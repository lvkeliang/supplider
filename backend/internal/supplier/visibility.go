// Visibility policy-tightening enforcement (可见性策略收紧 → 数据处置流程).
// When an admin lowers the maximum allowed visibility level, records whose
// visibility exceeds the new cap enter a disposition flow required by the
// PRD: 扫描不合规记录 → 通知录入者 → 7 天缓冲（标记"待调整"）→ 超时自动
// 降级到最近合规等级 → 支持申诉. Personal tier only exercises levels 0/1
// (max=1), but the state machine is tier-agnostic — enterprise runs the
// same code against all five levels.
//
// This is the non-AI, local base path: flagging is a document sub-record
// (NOT a status change — the supplier stays visible/usable throughout the
// buffer),通知在个人版表现为扫描报告/change_log/前端待调整徽标（企业版由
// 通知服务把同一份报告推送到钉钉/企微），所有跃迁写 change_log。
package supplier

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// DefaultBufferDays is the PRD-mandated cushion before auto-downgrade
// (7 天缓冲).
const DefaultBufferDays = 7

// Violation states (报告状态机).
const (
	ViolationNew      = "violation" // 新发现不合规，尚未标记（下次 enforce 即进入缓冲）
	ViolationPending  = "pending"   // 已标记待调整，缓冲期内
	ViolationAppealed = "appealed"  // 已申诉，倒计时暂停，等待管理员裁决
	ViolationOverdue  = "overdue"   // 缓冲期满未处理，下次 enforce 自动降级
)

// VisibilityPolicy is the admin-configured visibility rule (可见性策略):
// the highest visibility level records may use (0-4), plus the buffer days
// owners get before auto-downgrade. The policy is passed IN by the wiring
// layer (admin console / feature flags → HTTP/CLI); business code never
// reads tier config directly.
type VisibilityPolicy struct {
	MaxLevel   int `json:"max_level"`   // 允许的最高可见性等级
	BufferDays int `json:"buffer_days"` // 缓冲期天数（PRD = 7）
}

// normalized clamps the policy to legal values, applying defaults.
func (p VisibilityPolicy) normalized() VisibilityPolicy {
	if p.MaxLevel < domain.VisSelf {
		p.MaxLevel = domain.VisSelf
	}
	if p.MaxLevel > domain.VisCompany {
		p.MaxLevel = domain.VisCompany
	}
	if p.BufferDays <= 0 {
		p.BufferDays = DefaultBufferDays
	}
	return p
}

// VisibilityViolation is one non-compliant record in a scan report. It is
// also the "通知录入者" payload: personal tier shows it in the UI/CLI;
// enterprise pushes the same shape to the notification service.
type VisibilityViolation struct {
	SupplierID   string     `json:"supplier_id"`
	SupplierName string     `json:"supplier_name"`
	Owner        string     `json:"owner,omitempty"`
	Province     string     `json:"province,omitempty"`
	City         string     `json:"city,omitempty"`
	Visibility   int        `json:"visibility"`
	MaxLevel     int        `json:"max_level"`
	State        string     `json:"state"` // violation | pending | appealed | overdue
	FlaggedAt    *time.Time `json:"flagged_at,omitempty"`
	Deadline     *time.Time `json:"deadline,omitempty"`
	// DaysLeft counts days to the deadline. Negative = overdue that many
	// days; 0 = deadline day (still within buffer until end of day).
	DaysLeft int `json:"days_left,omitempty"`
}

// ScanVisibilityViolations is read-only: it returns every LIVE supplier
// whose visibility exceeds the policy cap and which holds no admin-approved
// exception, annotated with its current disposition state. Nothing is
// mutated — this is the 扫描/通知 basis and the pre-enforce preview.
func (s *Service) ScanVisibilityViolations(ctx context.Context, policy VisibilityPolicy) ([]VisibilityViolation, error) {
	policy = policy.normalized()
	today := truncToDate(s.now())

	out := make([]VisibilityViolation, 0)
	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.Items {
			if v, ok := violationOf(doc, policy, today); ok {
				out = append(out, v)
			}
		}
		if page.NextCursor == "" || len(out) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}
	sortViolations(out)
	return out, nil
}

// VisibilityEnforcementReport summarizes one enforce sweep (数据处置执行).
type VisibilityEnforcementReport struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Policy      VisibilityPolicy `json:"policy"`
	// Flagged: newly entered 待调整 (buffer started this run).
	Flagged int `json:"flagged"`
	// Pending: already flagged, still inside the buffer (untouched).
	Pending int `json:"pending"`
	// Appealed: appeal open — countdown paused, awaiting admin decision.
	Appealed int `json:"appealed"`
	// Downgraded: buffer expired (or appeal denied elsewhere) and the
	// record was auto-lowered to the nearest compliant level.
	Downgraded int `json:"downgraded"`
	// Resolved: owner adjusted visibility to a compliant level during the
	// buffer; the flag was cleared without downgrade.
	Resolved int `json:"resolved"`
	// Items is the REMAINING non-compliant set after the sweep (newly
	// flagged + pending + appealed), i.e. the current notification list.
	Items []VisibilityViolation `json:"items"`
}

// EnforceVisibilityPolicy runs one disposition sweep over live suppliers:
//
//  1. non-compliant, never flagged → mark 待调整, deadline = now + buffer
//     （通知录入者：change_log + 报告 + 前端待调整徽标）;
//  2. flagged, owner has since lowered visibility to a compliant level →
//     clear the flag (resolved, no downgrade);
//  3. flagged, deadline passed, no open appeal → auto-downgrade to the
//     policy cap (nearest compliant level);
//  4. appealed → left untouched (countdown paused);
//  5. admin-approved exception (申诉成立) → skipped entirely.
//
// Every transition appends change_log entries. The sweep is idempotent:
// re-running it on an unchanged dataset only re-counts states.
func (s *Service) EnforceVisibilityPolicy(ctx context.Context, policy VisibilityPolicy) (*VisibilityEnforcementReport, error) {
	policy = policy.normalized()
	now := s.now()
	today := truncToDate(now)
	rep := &VisibilityEnforcementReport{
		GeneratedAt: now,
		Policy:      policy,
		Items:       make([]VisibilityViolation, 0),
	}

	cursor := ""
	for {
		page, err := s.store.List(ctx, datamodel.Query{
			Limit:  datamodel.MaxPageSize,
			Cursor: cursor,
			Sort:   datamodel.Sort{Field: datamodel.SortCreatedAt, Order: datamodel.OrderDesc},
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range page.Items {
			changed := false
			enf := doc.VisEnforcement

			switch {
			case doc.Visibility <= policy.MaxLevel:
				// Compliant. A lingering flag means the record became
				// compliant during the buffer — close it out. The closure
				// reason matters for the audit trail: when an appeal was
				// open the owner's adjustment did not dismiss it, and it may
				// instead have been mooted by a loosened policy.
				if enf != nil && enf.Pending {
					doc.VisEnforcement = nil
					resolution := "resolved_owner_adjusted"
					if enf.Appealed {
						resolution = "resolved_appeal_moot_compliant"
					}
					doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
						Field: "vis_enforcement", Old: enforcementStateLabel(enf),
						New: resolution, Date: now, Source: domain.SourceSystem,
					})
					doc.UpdatedAt = now
					rep.Resolved++
					changed = true
				}
			case doc.VisException:
				// 申诉成立：admin-approved exception, sweep skips it.
			case enf == nil:
				// New violation: open the buffer (待调整).
				deadline := now.Add(time.Duration(policy.BufferDays) * 24 * time.Hour)
				reason := fmt.Sprintf("可见性策略收紧：等级 %d 超出最高允许等级 %d", doc.Visibility, policy.MaxLevel)
				doc.VisEnforcement = &domain.VisibilityEnforcement{
					Pending:            true,
					FlaggedAt:          now,
					Deadline:           deadline,
					PreviousVisibility: doc.Visibility,
					Reason:             reason,
				}
				doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
					Field:  "vis_enforcement",
					New:    fmt.Sprintf("待调整：%s，请于 %s 前调整，超时自动降级到等级 %d", reason, deadline.Format("2006-01-02"), policy.MaxLevel),
					Date:   now,
					Source: domain.SourceSystem,
				})
				doc.UpdatedAt = now
				rep.Flagged++
				changed = true
			case enf.Appealed:
				// 申诉中：倒计时暂停。
				rep.Appealed++
			default:
				// Flagged and not appealed — buffer running or expired.
				daysLeft := int(truncToDate(enf.Deadline).Sub(today).Hours() / 24)
				if daysLeft < 0 {
					// 超时：自动降级到最近合规等级（策略上限）。
					prev := doc.Visibility
					doc.Visibility = policy.MaxLevel
					doc.VisEnforcement = nil
					doc.ChangeLog = append(doc.ChangeLog,
						domain.ChangeEntry{
							Field: "visibility", Old: prev, New: policy.MaxLevel,
							Date: now, Source: domain.SourceSystem,
						},
						domain.ChangeEntry{
							Field: "vis_enforcement", Old: enforcementStateLabel(enf),
							New:  "auto_downgraded_timeout",
							Date: now, Source: domain.SourceSystem,
						},
					)
					doc.UpdatedAt = now
					rep.Downgraded++
					changed = true
				} else {
					rep.Pending++
				}
			}

			if changed {
				if err := s.store.Put(ctx, doc); err != nil {
					return nil, err
				}
			}
			// Remaining non-compliant records make the notification list.
			// Newly flagged docs appear as pending; downgraded/resolved docs
			// are compliant now and drop out naturally.
			if doc.Visibility > policy.MaxLevel && !doc.VisException {
				if v, ok := violationOf(doc, policy, today); ok {
					rep.Items = append(rep.Items, v)
				}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	sortViolations(rep.Items)
	return rep, nil
}

// AppealVisibility files an owner appeal (申诉) against a pending visibility
// adjustment. An open appeal pauses the auto-downgrade countdown until an
// admin resolves it. Idempotent: re-appealing returns the document unchanged.
func (s *Service) AppealVisibility(ctx context.Context, id, note string) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	enf := doc.VisEnforcement
	if enf == nil || !enf.Pending {
		return nil, fmt.Errorf("supplier: %s must be flagged as pending visibility adjustment (待调整) before an appeal can be filed", id)
	}
	if enf.Appealed {
		return doc, nil // idempotent
	}

	now := s.now()
	note = strings.TrimSpace(note)
	enf.Appealed = true
	enf.AppealNote = note
	enf.AppealedAt = &now
	summary := "申诉已提交：自动降级暂停，等待管理员裁决"
	if note != "" {
		summary = "申诉已提交：" + note
	}
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "vis_appeal", New: summary, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// ResolveVisibilityAppeal is the admin decision on an open appeal (申诉裁决):
// grant=true means 申诉成立 — the record keeps its level as an approved
// exception (vis_exception; re-flagging is skipped until visibility is next
// edited); grant=false means 驳回 — the record is downgraded to the policy
// cap immediately.
func (s *Service) ResolveVisibilityAppeal(ctx context.Context, id string, grant bool, policy VisibilityPolicy) (*domain.Supplier, error) {
	policy = policy.normalized()
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	enf := doc.VisEnforcement
	if enf == nil {
		return nil, fmt.Errorf("supplier: %s must be under visibility enforcement to resolve an appeal", id)
	}
	if !enf.Appealed {
		return nil, fmt.Errorf("supplier: %s must have an open visibility appeal (owner has not appealed) to resolve", id)
	}

	now := s.now()
	if grant {
		// 申诉成立：keep the level. Only stamp an exception while the
		// record is actually above the cap; a compliant record just closes.
		doc.VisEnforcement = nil
		if doc.Visibility > policy.MaxLevel {
			doc.VisException = true
			doc.ChangeLog = append(doc.ChangeLog,
				domain.ChangeEntry{
					Field: "vis_appeal", Old: "appealed", New: "granted_exception",
					Date: now, Source: domain.SourceManual,
				},
				domain.ChangeEntry{
					Field: "vis_exception", New: true,
					Date: now, Source: domain.SourceManual,
				},
			)
		} else {
			doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
				Field: "vis_appeal", Old: "appealed", New: "granted_compliant",
				Date: now, Source: domain.SourceManual,
			})
		}
	} else {
		// 申诉驳回：downgrade to the nearest compliant level immediately.
		// If the record is already compliant (e.g. policy loosened while
		// the appeal was open), only close it — never fabricate a no-op
		// visibility change in the audit trail.
		prev := doc.Visibility
		doc.VisEnforcement = nil
		entry := domain.ChangeEntry{
			Field: "vis_appeal", Old: "appealed", New: "denied_downgraded",
			Date: now, Source: domain.SourceManual,
		}
		if prev <= policy.MaxLevel {
			entry.New = "denied_closed_compliant"
			doc.ChangeLog = append(doc.ChangeLog, entry)
		} else {
			doc.Visibility = policy.MaxLevel
			doc.ChangeLog = append(doc.ChangeLog, entry, domain.ChangeEntry{
				Field: "visibility", Old: prev, New: policy.MaxLevel,
				Date: now, Source: domain.SourceManual,
			})
		}
	}
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// violationOf projects a live document onto the scan shape, reporting
// false for compliant / exception records.
func violationOf(doc *domain.Supplier, policy VisibilityPolicy, today time.Time) (VisibilityViolation, bool) {
	if doc.VisException || doc.Visibility <= policy.MaxLevel {
		return VisibilityViolation{}, false
	}
	v := VisibilityViolation{
		SupplierID:   doc.ID,
		SupplierName: doc.BasicInfo.CompanyName,
		Owner:        doc.Owner,
		Province:     doc.BasicInfo.Region.Province,
		City:         doc.BasicInfo.Region.City,
		Visibility:   doc.Visibility,
		MaxLevel:     policy.MaxLevel,
		State:        ViolationNew,
	}
	if enf := doc.VisEnforcement; enf != nil && enf.Pending {
		v.FlaggedAt = &enf.FlaggedAt
		v.Deadline = &enf.Deadline
		v.DaysLeft = int(truncToDate(enf.Deadline).Sub(today).Hours() / 24)
		switch {
		case enf.Appealed:
			v.State = ViolationAppealed
		case v.DaysLeft < 0:
			v.State = ViolationOverdue
		default:
			v.State = ViolationPending
		}
	}
	return v, true
}

// enforcementStateLabel renders the pre-transition state for change_log
// (e.g. "pending_adjustment" / "appealed").
func enforcementStateLabel(enf *domain.VisibilityEnforcement) string {
	if enf == nil {
		return ""
	}
	if enf.Appealed {
		return "appealed"
	}
	return "pending_adjustment"
}

// sortViolations orders the report by urgency: overdue first (most overdue
// first), then pending (nearest deadline first), then unflagged violations,
// appealed last (paused, no countdown).
func sortViolations(vs []VisibilityViolation) {
	rank := map[string]int{
		ViolationOverdue:  0,
		ViolationPending:  1,
		ViolationNew:      2,
		ViolationAppealed: 3,
	}
	sort.Slice(vs, func(i, j int) bool {
		ri, rj := rank[vs[i].State], rank[vs[j].State]
		if ri != rj {
			return ri < rj
		}
		if ri == rank[ViolationOverdue] || ri == rank[ViolationPending] {
			if vs[i].DaysLeft != vs[j].DaysLeft {
				return vs[i].DaysLeft < vs[j].DaysLeft
			}
		}
		if vs[i].SupplierName != vs[j].SupplierName {
			return vs[i].SupplierName < vs[j].SupplierName
		}
		return vs[i].SupplierID < vs[j].SupplierID
	})
}

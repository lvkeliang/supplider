// Shell-company risk detection (空壳特征检测): the non-AI "审核" phase rule
// engine. The pure rules live in internal/risk; this file wires them into
// the supplier lifecycle so every write carries an up-to-date risk_flags,
// and exposes a live review queue.
//
// This is the degradable base path: no network, no API keys, no model. The
// [AI] 空壳风险报告 later layers LLM analysis on top of THESE signals; with
// no key configured this engine alone drives shell_risk, satisfying the
// "AI 原生但可降级" principle.
package supplier

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/risk"
)

// applyRisk runs the local rule engine against the document and writes the
// shell-company verdict onto RiskFlags. It is called on every Create/Update,
// alongside recomputeRating — like the rating, risk_flags is DERIVED data
// (recomputed from the document), so it produces no change_log noise.
//
// Only the local-engine fields (ShellRisk, Notes) are touched. External
// signals populated by higher tiers (ExecutedPerson 被执行人 / AdminPenalty
// 行政处罚 via 企查查/天眼查 APIs) are preserved across re-evaluation.
func (s *Service) applyRisk(doc *domain.Supplier) {
	rep := risk.Evaluate(doc, s.now())
	doc.RiskFlags.ShellRisk = rep.ShellRisk
	doc.RiskFlags.Notes = rep.Notes()
}

// RiskReport runs the rule engine against the stored document and returns
// the full signal list WITHOUT persisting. Signals are transient (the
// document only stores the denormalized shell_risk flag + notes), so the
// detail UI/CLI calls this to explain WHY a supplier was flagged. Pure and
// deterministic — safe to call on every detail view.
func (s *Service) RiskReport(ctx context.Context, id string) (*risk.Report, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rep := risk.Evaluate(doc, s.now())
	return &rep, nil
}

// CheckRisks re-runs the rule engine against one supplier and persists the
// refreshed verdict. It is the on-demand / backfill entry point (rules
// improve over time; this brings an older document's stored risk_flags up
// to date without requiring an edit). Returns the live report.
func (s *Service) CheckRisks(ctx context.Context, id string) (*risk.Report, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rep := risk.Evaluate(doc, s.now())
	if doc.RiskFlags.ShellRisk != rep.ShellRisk || doc.RiskFlags.Notes != rep.Notes() {
		doc.RiskFlags.ShellRisk = rep.ShellRisk
		doc.RiskFlags.Notes = rep.Notes()
		doc.UpdatedAt = s.now()
		if err := s.store.Put(ctx, doc); err != nil {
			return nil, err
		}
	}
	return &rep, nil
}

// ShellRiskItem is one supplier flagged for manual review (审核队列), with
// the fired signals carried so the UI can show reasons without a second
// round-trip.
type ShellRiskItem struct {
	SupplierID   string        `json:"supplier_id"`
	SupplierName string        `json:"supplier_name"`
	Province     string        `json:"province,omitempty"`
	City         string        `json:"city,omitempty"`
	HighCount    int           `json:"high_count"`
	MediumCount  int           `json:"medium_count"`
	Signals      []risk.Signal `json:"signals"`
}

// ShellRiskSuppliers scans ACTIVE suppliers (keyset-paginated full
// documents, same walk pattern as Export / ExpiringQualifications),
// evaluates the local rules LIVE and returns every supplier the engine
// flags as shell-risk. Evaluation is live rather than reading the stored
// flag so the queue always reflects the current rules and needs no backfill.
// Sorted most-suspicious first: high-severity signals, then mediums, then
// name. Archived suppliers are excluded (history retained, no review noise).
func (s *Service) ShellRiskSuppliers(ctx context.Context) ([]ShellRiskItem, error) {
	items := make([]ShellRiskItem, 0)
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
			// A human has already reviewed and cleared this supplier — it
			// leaves the actionable queue (the list card stops badging too).
			if doc.RiskFlags.Reviewed {
				continue
			}
			rep := risk.Evaluate(doc, s.now())
			if !rep.ShellRisk {
				continue
			}
			item := ShellRiskItem{
				SupplierID:   doc.ID,
				SupplierName: doc.BasicInfo.CompanyName,
				Province:     doc.BasicInfo.Region.Province,
				City:         doc.BasicInfo.Region.City,
				Signals:      rep.Signals,
			}
			for _, sig := range rep.Signals {
				switch sig.Severity {
				case risk.SevHigh:
					item.HighCount++
				case risk.SevMedium:
					item.MediumCount++
				}
			}
			items = append(items, item)
		}
		if page.NextCursor == "" || len(items) >= MaxExportDocs {
			break
		}
		cursor = page.NextCursor
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].HighCount != items[j].HighCount {
			return items[i].HighCount > items[j].HighCount
		}
		if items[i].MediumCount != items[j].MediumCount {
			return items[i].MediumCount > items[j].MediumCount
		}
		return items[i].SupplierName < items[j].SupplierName
	})
	return items, nil
}

// RiskReviewInput carries a human's resolution of a flagged supplier.
type RiskReviewInput struct {
	// Outcome is domain.RiskReviewVerified (已核验) or RiskReviewDismissed
	// (误报忽略).
	Outcome string
	By      string // reviewer id (personal tier: local user)
	Note    string // optional free-text note
}

// ReviewRisk records a human's resolution of the local-rule shell verdict
// (人工审核闭环): the rule engine flags; a person clears the flag after
// inspecting the dossier / paper certificates. The resolution is recorded
// on RiskFlags and in change_log (provenance). A reviewed supplier leaves
// the review queue until a risk-relevant edit reopens it (see
// reopenRiskReviewIfNeeded). The engine verdict (shell_risk/notes) is left
// intact — it stays objectively true; "reviewed" means a person accepted it.
func (s *Service) ReviewRisk(ctx context.Context, id string, in RiskReviewInput) (*domain.Supplier, error) {
	outcome := strings.TrimSpace(in.Outcome)
	if outcome != domain.RiskReviewVerified && outcome != domain.RiskReviewDismissed {
		return nil, fmt.Errorf(
			"supplier: review outcome must be %q or %q, got %q",
			domain.RiskReviewVerified, domain.RiskReviewDismissed, outcome)
	}
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	now := s.now()
	prev := doc.RiskFlags.ReviewOutcome
	doc.RiskFlags.Reviewed = true
	at := now
	doc.RiskFlags.ReviewedAt = &at
	doc.RiskFlags.ReviewedBy = strings.TrimSpace(in.By)
	doc.RiskFlags.ReviewOutcome = outcome
	doc.RiskFlags.ReviewNote = strings.TrimSpace(in.Note)
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field:  "risk_review",
		Old:    prev,
		New:    outcome,
		Date:   now,
		Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// RiskFlagsInput carries manual external risk signals to set/clear
// (ExecutedPerson 被执行人 / AdminPenalty 行政处罚). The personal tier has no
// 企查查/天眼查 API, so this manual path is how a 淘汰-phase 风险预警 is recorded;
// higher tiers populate the same fields automatically and the local engine
// never overwrites them (applyRisk only touches shell_risk/notes).
type RiskFlagsInput struct {
	ExecutedPerson *bool `json:"executed_person"`
	AdminPenalty   *bool `json:"admin_penalty"`
}

// SetRiskFlags records external risk signals on a supplier. Only the provided
// signals are touched; the local-engine verdicts (shell_risk / notes / review)
// are preserved. Changes are written to change_log (source=manual). A no-op
// (nothing changed) returns the current document without persisting.
func (s *Service) SetRiskFlags(ctx context.Context, id string, in RiskFlagsInput) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	changed := false
	if in.ExecutedPerson != nil && *in.ExecutedPerson != doc.RiskFlags.ExecutedPerson {
		doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
			Field: "risk_flags.executed_person", Old: doc.RiskFlags.ExecutedPerson, New: *in.ExecutedPerson, Date: now, Source: domain.SourceManual,
		})
		doc.RiskFlags.ExecutedPerson = *in.ExecutedPerson
		changed = true
	}
	if in.AdminPenalty != nil && *in.AdminPenalty != doc.RiskFlags.AdminPenalty {
		doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
			Field: "risk_flags.admin_penalty", Old: doc.RiskFlags.AdminPenalty, New: *in.AdminPenalty, Date: now, Source: domain.SourceManual,
		})
		doc.RiskFlags.AdminPenalty = *in.AdminPenalty
		changed = true
	}
	if !changed {
		return doc, nil
	}
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// reopenRiskReview clears a prior human review when an Update touches a
// field the rule engine reads, so a stale clearance can never hide a NEW
// signal. Risk inputs are basic_info (credit code, dates, capital, …),
// qualifications and categories; edits to performance, products,
// custom_fields, visibility or attachments do NOT reopen (they cannot change
// the shell verdict).
func reopenRiskReviewIfNeeded(doc *domain.Supplier, changes []domain.ChangeEntry) {
	riskRelevant := false
	for _, c := range changes {
		if strings.HasPrefix(c.Field, "basic_info.") ||
			c.Field == "qualifications" || c.Field == "categories" {
			riskRelevant = true
			break
		}
	}
	if !riskRelevant || !doc.RiskFlags.Reviewed {
		return
	}
	doc.RiskFlags.Reviewed = false
	doc.RiskFlags.ReviewedAt = nil
	doc.RiskFlags.ReviewedBy = ""
	doc.RiskFlags.ReviewOutcome = ""
	doc.RiskFlags.ReviewNote = ""
}

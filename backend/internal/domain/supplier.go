// Package domain holds the supplier document model — the single source of
// truth for supplier data across all tiers.
//
// The model is document-style (文档式存储): a fixed core plus free-form
// custom_fields, because supplier shapes differ wildly by industry
// (施工商: 资质/设备/垫资能力; 贸易商: 品牌/规格/价格; 定制开发: 技术栈/案例).
//
// Business code depends on these types and on the datamodel interfaces —
// never on a concrete database driver.
package domain

import "time"

// Lifecycle statuses. Delete is ARCHIVE semantics: history is retained.
const (
	StatusActive      = "active"      // 在库
	StatusPending     = "pending"     // 待审核 / 待调整
	StatusArchived    = "archived"    // 归档（删除语义，保留历史）
	StatusBlacklisted = "blacklisted" // 黑名单
)

// Five-level visibility (PRD 五级可见性权限体系).
// Personal tier enables levels 0/1 only; enterprise enables all.
const (
	VisSelf       = 0 // 仅自己可见
	VisNamedUsers = 1 // 指定人可见
	VisDepartment = 2 // 本部门可见
	VisNamedDepts = 3 // 指定部门可见
	VisCompany    = 4 // 全公司可见
)

// Change-log sources.
const (
	SourceManual    = "manual"     // 手工编辑
	SourceImport    = "import"     // Excel 导入
	SourceAPISync   = "api_sync"   // 工商变更监控同步
	SourceAIExtract = "ai_extract" // AI 抽取（增强层，可降级）
	SourceSystem    = "system"     // 系统自动执行（策略收紧处置/定时任务）
)

// Supplier is one supplier document.
type Supplier struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Status string `json:"status"`

	BasicInfo BasicInfo `json:"basic_info"`

	Qualifications     []Qualification  `json:"qualifications,omitempty"`
	Categories         []string         `json:"categories,omitempty"`
	ProductsServices   []ProductService `json:"products_services,omitempty"`
	PerformanceHistory []Performance    `json:"performance_history,omitempty"`
	RiskFlags          RiskFlags        `json:"risk_flags,omitempty"`

	// Visibility: 0-4 (see Vis* constants). SharedWith lists user/dept ids
	// for levels 1 (users) and 3 (departments).
	Visibility int      `json:"visibility"`
	SharedWith []string `json:"shared_with,omitempty"`

	// VisEnforcement is present while the supplier is inside the
	// visibility-policy-tightening disposition flow (可见性策略收紧数据处置):
	// flagged 待调整 → buffer countdown → owner adjusts / appeals / timeout
	// auto-downgrade. It is a sub-document rather than a status change so the
	// supplier stays visible/usable throughout the buffer. Absent once the
	// record is compliant again; every transition is in change_log.
	VisEnforcement *VisibilityEnforcement `json:"vis_enforcement,omitempty"`
	// VisException records an admin-approved exception to the visibility
	// policy (申诉成立): the record may keep a level above the policy max.
	// Cleared when visibility is next edited — the exception applied to the
	// level that was approved, not to any future level.
	VisException bool `json:"vis_exception,omitempty"`

	// CustomFields is free-form. Field names and value types are NOT
	// predefined — the UI adds them dynamically. Values may be strings,
	// numbers, booleans or string arrays.
	CustomFields map[string]any `json:"custom_fields,omitempty"`

	ChangeLog   []ChangeEntry `json:"change_log,omitempty"`
	Attachments []Attachment  `json:"attachments,omitempty"`

	// Rating is the aggregate performance score (0-5), computed from
	// PerformanceHistory. Denormalized for sorting/filtering.
	Rating float64 `json:"rating,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	// BlacklistReason records why a supplier was moved to the blacklist
	// (status=blacklisted, 淘汰阶段): e.g. confirmed fraud, serious breach.
	// Cleared when the supplier is removed from the blacklist. The date and
	// actor are in change_log.
	BlacklistReason string `json:"blacklist_reason,omitempty"`

	// Watched marks a supplier the current user follows (关注). When a
	// watched supplier changes (risk flag, blacklist, archive, merge, a
	// qualification nearing expiry) an in-app notification is raised. It is
	// personal UI state — toggling it writes no change_log and does not bump
	// UpdatedAt. The same notification payload is what enterprise pushes to
	// 钉钉/企微; the personal tier surfaces it in an in-app bell (可降级).
	Watched bool `json:"watched,omitempty"`
}

// BasicInfo is the fixed core of every supplier.
type BasicInfo struct {
	CompanyName       string `json:"company_name"`
	CreditCode        string `json:"credit_code,omitempty"`  // 统一社会信用代码
	LegalPerson       string `json:"legal_person,omitempty"` // 法定代表人
	RegisteredCapital string `json:"registered_capital,omitempty"`
	EstablishmentDate string `json:"establishment_date,omitempty"` // ISO date, as registered
	BusinessScope     string `json:"business_scope,omitempty"`
	SupplierType      string `json:"supplier_type,omitempty"` // construction | trade | custom_dev | service | ...
	ContactName       string `json:"contact_name,omitempty"`
	ContactPhone      string `json:"contact_phone,omitempty"`
	ContactEmail      string `json:"contact_email,omitempty"`
	Region            Region `json:"region"`
	Address           string `json:"address,omitempty"`
	Website           string `json:"website,omitempty"`
}

// Region is the structured locality used for 地域 filtering and local-supplier
// preference. Province/City are short names ("浙江", "杭州").
type Region struct {
	Province string `json:"province"`
	City     string `json:"city"`
	District string `json:"district,omitempty"`
}

// Qualification is one certificate (资质). Level uses Chinese construction
// ranks: 特级/一级/二级/三级 — rank ordering via QualRank.
type Qualification struct {
	Type      string `json:"type"`            // e.g. 建筑工程施工总承包
	Level     string `json:"level,omitempty"` // 特级/一级/二级/三级
	CertNo    string `json:"cert_no,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
	IssueDate string `json:"issue_date,omitempty"`
	Expiry    string `json:"expiry,omitempty"` // ISO date; drives 90/30/7-day reminders
	Verified  bool   `json:"verified"`
}

// ProductService is one offered product or service line.
type ProductService struct {
	Name           string `json:"name"`
	Desc           string `json:"desc,omitempty"`
	UnitPriceRange string `json:"unit_price_range,omitempty"`
}

// Performance is one past project collaboration record.
type Performance struct {
	Project     string  `json:"project"`
	Date        string  `json:"date,omitempty"`        // free-form month/date, e.g. "2025-08"
	Score       float64 `json:"score"`                 // 0-5 overall
	Delivery    float64 `json:"delivery,omitempty"`    // 交付
	Quality     float64 `json:"quality,omitempty"`     // 质量
	Cooperation float64 `json:"cooperation,omitempty"` // 配合度
	Feedback    string  `json:"feedback,omitempty"`
}

// MaxScore is the upper bound for the overall and per-dimension scores.
const MaxScore = 5.0

// EffectiveScore is the record's contribution to the aggregate rating:
// the explicit overall score wins; otherwise it is the mean of whichever
// 交付/质量/配合度 dimensions were set. A record with no scores returns 0
// (callers exclude it from the average).
func (p Performance) EffectiveScore() float64 {
	if p.Score > 0 {
		return p.Score
	}
	var sum float64
	n := 0
	for _, v := range []float64{p.Delivery, p.Quality, p.Cooperation} {
		if v > 0 {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// Risk review outcomes (人工审核闭环). A reviewer resolves a flagged
// supplier once they have inspected the dossier / paper certificates.
const (
	RiskReviewVerified  = "verified"  // 已核验：人工查验原件，确认为正规供应商
	RiskReviewDismissed = "dismissed" // 误报忽略：规则误判，无需处理
)

// RiskFlags carries shell-company / enforcement signals populated by the
// risk rule engine or external APIs (small_business tier and later).
type RiskFlags struct {
	ShellRisk      bool   `json:"shell_risk"`
	ExecutedPerson bool   `json:"executed_person"` // 被执行人
	AdminPenalty   bool   `json:"admin_penalty"`   // 行政处罚
	Notes          string `json:"notes,omitempty"`

	// Human review of the local-rule shell verdict (人工审核). The engine
	// only flags; a person clears the queue. Reviewed is reset to false when
	// a risk-relevant field (basic_info / qualifications / categories) is
	// edited afterwards, so a stale clearance never hides a new signal.
	Reviewed      bool       `json:"reviewed,omitempty"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy    string     `json:"reviewed_by,omitempty"`
	ReviewOutcome string     `json:"review_outcome,omitempty"` // verified | dismissed
	ReviewNote    string     `json:"review_note,omitempty"`
}

// VisibilityEnforcement tracks one supplier through the data-disposition
// flow triggered when an admin tightens the visibility policy (策略收紧):
// records above the new maximum level are flagged 待调整 with a deadline
// (PRD: 7-day buffer); the owner adjusts the level themselves (flag cleared
// by the next sweep), files an appeal (申诉, pauses auto-downgrade), or is
// auto-downgraded to the nearest compliant level when the deadline passes.
// Personal tier only ever exercises levels 0/1, but the state machine is
// tier-agnostic.
type VisibilityEnforcement struct {
	// Pending is true while the record is 待调整 (flagged, awaiting owner
	// adjustment or appeal resolution).
	Pending   bool      `json:"pending_adjustment"`
	FlaggedAt time.Time `json:"flagged_at"`
	Deadline  time.Time `json:"deadline"`
	// PreviousVisibility is the level at flag time, kept for the appeal
	// context and for change-log history.
	PreviousVisibility int    `json:"previous_visibility"`
	Reason             string `json:"reason,omitempty"`

	// Appeal state (申诉). An open appeal pauses the downgrade countdown —
	// an appealed record is never auto-downgraded; an admin resolves it
	// (grant = exception, keep level; deny = downgrade immediately).
	Appealed   bool       `json:"appealed,omitempty"`
	AppealNote string     `json:"appeal_note,omitempty"`
	AppealedAt *time.Time `json:"appealed_at,omitempty"`
}

// ChangeEntry records one field change. Old/New are the previous/new values
// (nil Old means creation). Source is one of the Source* constants.
type ChangeEntry struct {
	Field  string    `json:"field"`
	Old    any       `json:"old,omitempty"`
	New    any       `json:"new,omitempty"`
	Date   time.Time `json:"date"`
	Source string    `json:"source"`
}

// Attachment is a stored file. Personal tier keeps files on the local FS
// (URL is a local path/URI); enterprise uses S3-compatible storage.
// Hard limit: 50MB per file (enforced at the HTTP layer).
type Attachment struct {
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	MIMEType   string    `json:"mime_type,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// Summary is the list-page projection. Performance red line: list endpoints
// return summary fields only — never full documents.
type Summary struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Province   string   `json:"province"`
	City       string   `json:"city"`
	District   string   `json:"district,omitempty"`
	Categories []string `json:"categories,omitempty"`
	TopQual    string   `json:"top_qual,omitempty"` // highest qualification label
	Rating     float64  `json:"rating"`
	Status     string   `json:"status"`
	// ShellRisk is the denormalized local-rule shell-company flag (空壳风险),
	// set on write so the list can badge risky suppliers without a scan.
	ShellRisk bool `json:"shell_risk,omitempty"`
	// RiskReviewed mirrors the human-review state; the list badges only
	// UN-reviewed risk (shell_risk && !risk_reviewed = needs attention).
	RiskReviewed bool `json:"risk_reviewed,omitempty"`
	// VisPending mirrors vis_enforcement.pending_adjustment: the record is
	// 待调整 under a tightened visibility policy (buffer countdown running).
	VisPending bool `json:"vis_pending,omitempty"`
	// Watched mirrors the follow flag for list badges / watched-only filter.
	Watched   bool      `json:"watched,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToSummary projects a full document onto the list-page shape.
func ToSummary(s *Supplier) Summary {
	top, _ := TopQualification(s)
	return Summary{
		ID:           s.ID,
		Name:         s.BasicInfo.CompanyName,
		Province:     s.BasicInfo.Region.Province,
		City:         s.BasicInfo.Region.City,
		District:     s.BasicInfo.Region.District,
		Categories:   s.Categories,
		TopQual:      top,
		Rating:       s.Rating,
		Status:       s.Status,
		ShellRisk:    s.RiskFlags.ShellRisk,
		RiskReviewed: s.RiskFlags.Reviewed,
		VisPending:   s.VisEnforcement != nil && s.VisEnforcement.Pending,
		Watched:      s.Watched,
		UpdatedAt:    s.UpdatedAt,
	}
}

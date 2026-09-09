// Package risk implements the non-AI shell-company detection rules
// (空壳特征检测) for the supplier lifecycle "审核" phase.
//
// Every rule is a pure, local, explainable heuristic — no network, no API
// keys, no model. The [AI] risk report (LLM 空壳风险报告) later layers on
// top of THESE signals; without a key this engine alone drives the
// shell_risk flag, satisfying the "AI 原生但可降级" design principle.
//
// Signals are intentionally conservative: they flag patterns for a human
// to review, they never block entry. A high-severity signal (or two
// mediums) sets RiskFlags.ShellRisk so the UI/CLI can route the supplier
// into manual approval.
package risk

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/domain"
)

type Severity string

const (
	SevHigh   Severity = "high"   // 直接计入空壳风险
	SevMedium Severity = "medium" // 需人工复核；两条即计入空壳风险
	SevLow    Severity = "low"    // 提示补全资料
)

// Signal is one fired rule. Codes are stable identifiers (R1xx = identity
// documents, R2xx = profile/age, R3xx = financial) so the UI and future
// rule versions can reference them.
type Signal struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"` // 中文解释，直接展示给用户
}

// Report is one evaluation result.
type Report struct {
	ShellRisk bool      `json:"shell_risk"`
	Signals   []Signal  `json:"signals"`
	CheckedAt time.Time `json:"checked_at"`
}

// Rule thresholds (conservative; tunable without touching logic).
const (
	// YoungCompanyDays: a company younger than this at entry time is a
	// classic shell-buying pattern (临时注册).
	YoungCompanyDays = 180
	// ThinProfileMissing: this many missing core profile fields counts as
	// a thin profile (资料过于简陋).
	ThinProfileMissing = 2
	// LowCapitalYuan: registered capital below this (100 万元) is unusually
	// thin for a construction supplier.
	LowCapitalYuan = 1_000_000.0
)

// Evaluate runs every local rule against the supplier document. It never
// mutates the document and never errors — rules that cannot evaluate (e.g.
// unparseable date) simply do not fire.
func Evaluate(s *domain.Supplier, now time.Time) Report {
	rep := Report{Signals: []Signal{}, CheckedAt: now.UTC()}
	b := s.BasicInfo

	// ---- R1xx: identity documents (统一社会信用代码) ----
	code := strings.TrimSpace(strings.ToUpper(b.CreditCode))
	switch {
	case code == "":
		rep.add(Signal{"R101", SevLow, "未填写统一社会信用代码，建议补全以便核验"})
	case isLegacyRegistrationNumber(code):
		// A 15-digit all-digit value is the PRE-2015 工商注册号 (business
		// registration number), issued before 三证合一 replaced it with the
		// 18-char unified code. Real companies legitimately carry it; it is
		// not a forgery, so it is a data-completeness hint, never a shell flag.
		rep.add(Signal{"R104", SevLow, "填写的是 15 位旧版工商注册号，建议补全为 18 位统一社会信用代码以便核验"})
	case len(code) != 18:
		rep.add(Signal{"R102", SevHigh, fmt.Sprintf(
			"统一社会信用代码长度为 %d 位，应为 18 位（疑似伪造或录入错误）", len(code))})
	case !creditCodeCharset.MatchString(code):
		rep.add(Signal{"R102", SevHigh, "统一社会信用代码含非法字符（标准代码仅含数字与大写字母，不含 I/O/S/V/Z）"})
	case !CheckCreditCode(code):
		rep.add(Signal{"R103", SevHigh, "统一社会信用代码校验位不符（GB 32100-2015），真实代码无法通过校验，疑似编造"})
	}

	// ---- R2xx: profile / age ----
	if est, ok := parseISODate(b.EstablishmentDate); ok {
		ageDays := int(now.UTC().Sub(est).Hours() / 24)
		if ageDays >= 0 && ageDays < YoungCompanyDays {
			rep.add(Signal{"R201", SevMedium, fmt.Sprintf(
				"公司成立仅 %d 天（未满 %d 天），新注册主体需关注空壳/买壳风险", ageDays, YoungCompanyDays)})
		}
	}

	missing := []string{}
	if strings.TrimSpace(b.LegalPerson) == "" {
		missing = append(missing, "法定代表人")
	}
	if strings.TrimSpace(b.ContactPhone) == "" && strings.TrimSpace(b.ContactName) == "" {
		missing = append(missing, "联系人/电话")
	}
	if strings.TrimSpace(b.RegisteredCapital) == "" {
		missing = append(missing, "注册资本")
	}
	if strings.TrimSpace(b.BusinessScope) == "" {
		missing = append(missing, "经营范围")
	}
	if len(missing) >= ThinProfileMissing {
		rep.add(Signal{"R202", SevMedium, "档案资料过于简陋，缺少：" + strings.Join(missing, "、")})
	}

	if isConstruction(s) && len(s.Qualifications) == 0 {
		rep.add(Signal{"R203", SevMedium, "施工类供应商未登记任何资质证书，无法承接相应工程，需重点核验"})
	}

	if dup := duplicateQualifications(s.Qualifications); dup != "" {
		rep.add(Signal{"R204", SevMedium, "资质登记重复（" + dup + "），同一证书重复录入常用于堆砌资质，需核验原件"})
	}

	// ---- R3xx: financial ----
	if capYuan, ok := parseRegisteredCapital(b.RegisteredCapital); ok {
		if capYuan < LowCapitalYuan {
			rep.add(Signal{"R301", SevLow, fmt.Sprintf(
				"注册资本 %.0f 元（低于 %.0f 元），承接大型项目的垫资/履约能力存疑",
				capYuan, LowCapitalYuan)})
		}
	}

	// Shell risk: any high signal, or two+ mediums.
	high, medium := 0, 0
	for _, sig := range rep.Signals {
		switch sig.Severity {
		case SevHigh:
			high++
		case SevMedium:
			medium++
		}
	}
	rep.ShellRisk = high > 0 || medium >= 2
	return rep
}

func (r *Report) add(sig Signal) { r.Signals = append(r.Signals, sig) }

// Notes renders the human-facing summary stored in RiskFlags.Notes.
// Only medium/high signals go in; low signals are UI hints, not risk notes.
func (r *Report) Notes() string {
	parts := make([]string, 0, len(r.Signals))
	for _, sig := range r.Signals {
		if sig.Severity == SevLow {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", sig.Code, sig.Message))
	}
	if len(parts) == 0 {
		return ""
	}
	return "自动检测 " + strings.Join(parts, "；")
}

// isConstruction reports whether the supplier presents itself as a
// construction contractor (the industry this MVP launches in).
func isConstruction(s *domain.Supplier) bool {
	if strings.Contains(s.BasicInfo.SupplierType, "施工") || strings.Contains(s.BasicInfo.SupplierType, "建筑") {
		return true
	}
	for _, c := range s.Categories {
		if strings.Contains(c, "施工") || strings.Contains(c, "建筑") {
			return true
		}
	}
	return false
}

// duplicateQualifications returns a human-readable description of the first
// repeated certificate it finds, or "" when none. Two qualifications count
// as duplicates when they share a non-empty certificate number (same cert
// entered twice — definitive) or the same type AND level (same certificate
// class entered twice — resume-padding / data-entry smell).
func duplicateQualifications(quals []domain.Qualification) string {
	certNo := map[string]int{}
	typeLevel := map[string]int{}
	for _, q := range quals {
		if no := strings.TrimSpace(q.CertNo); no != "" {
			certNo[no]++
			if certNo[no] == 2 {
				return "证书号 " + no + " 出现多次"
			}
		}
		t := strings.TrimSpace(q.Type)
		l := strings.TrimSpace(q.Level)
		if t != "" {
			key := t + "/" + l
			typeLevel[key]++
			if typeLevel[key] == 2 {
				if l == "" {
					return "资质「" + t + "」出现多次"
				}
				return "资质「" + t + " " + l + "」出现多次"
			}
		}
	}
	return ""
}

// ---- unified social credit code (GB 32100-2015) ----

// creditCodeCharset: 18 chars total; the alphabet is digits + uppercase
// letters EXCLUDING I, O, S, V, Z (31 symbols).
var creditCodeCharset = regexp.MustCompile(`^[0-9A-HJ-NPQRTUWXY]{18}$`)

// creditCodeAlphabet maps each symbol to its value (index in the GB set).
var creditCodeAlphabet = map[byte]int{}

func init() {
	const alphabet = "0123456789ABCDEFGHJKLMNPQRTUWXY"
	for i := 0; i < len(alphabet); i++ {
		creditCodeAlphabet[alphabet[i]] = i
	}
}

// creditCodeWeights are the per-position weights for the first 17 chars.
var creditCodeWeights = []int{1, 3, 9, 27, 19, 26, 16, 17, 20, 29, 25, 13, 8, 24, 10, 30, 28}

// CheckCreditCode validates an 18-char unified social credit code's
// checksum per GB 32100-2015: C = (31 - Σ(v_i·w_i) mod 31) mod 31, then
// the 18th character must equal alphabet[C].
func CheckCreditCode(code string) bool {
	if len(code) != 18 {
		return false
	}
	sum := 0
	for i := 0; i < 17; i++ {
		v, ok := creditCodeAlphabet[code[i]]
		if !ok {
			return false
		}
		sum += v * creditCodeWeights[i]
	}
	check := (31 - sum%31) % 31
	want, ok := creditCodeAlphabet[code[17]]
	return ok && want == check
}

// ---- loose local parsers (never fatal) ----

// dateLayouts lists accepted establishment-date spellings. Chinese-locale
// forms (slash/dot/年月日, padding optional) parse the same as ISO so the
// young-company rule still evaluates imported/spreadsheet data instead of
// silently skipping it.
var dateLayouts = []string{
	"2006-1-2",
	"2006/1/2",
	"2006.1.2",
	"2006年1月2日",
}

func parseISODate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

var capitalRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*(亿|万)?`)

// parseRegisteredCapital extracts registered capital in YUAN from strings
// like "1000万人民币", "500万元", "100万", "1.2亿". Bare numbers are
// treated as yuan. Returns false when no number is present.
// isLegacyRegistrationNumber reports a 15-digit all-numeric pre-2015
// business registration number (工商注册号), the standard the unified
// social credit code replaced in the 三证合一 reform.
func isLegacyRegistrationNumber(code string) bool {
	if len(code) != 15 {
		return false
	}
	for i := 0; i < 15; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func parseRegisteredCapital(s string) (float64, bool) {
	// Tolerate thousands separators: "1,000万" / "1，000.50 万元" must not
	// collapse to the leading "1" (a false low-capital signal).
	s = strings.NewReplacer(",", "", "，", "").Replace(strings.TrimSpace(s))
	m := capitalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(m[1], "%f", &v); err != nil {
		return 0, false
	}
	switch m[2] {
	case "万":
		v *= 1e4
	case "亿":
		v *= 1e8
	}
	return v, true
}

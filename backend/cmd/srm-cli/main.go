// Command srm-cli is the Supplider command-line client. It speaks the same
// local HTTP API the desktop app uses (default 127.0.0.1:7612, started by
// the Tauri sidecar) — so it works against exactly the data the UI shows.
//
// This same binary is later exposed through MCP as Tools/Resources;
// commands map 1:1 to MCP tools.
//
// Implemented (MVP seed):
//
//	srm-cli add <file.json|-> [--visibility N] [--owner user]
//	srm-cli list [filters] [--limit N] [--cursor CURSOR] [--json]
//	srm-cli search <keyword> [filters]   (FTS backend lands later; same flags)
//	srm-cli info <id> [--json]
//	srm-cli export [--format json|xlsx] [--out file] [filters] [--include-archived]
//	srm-cli compare <id> <id>... [--criteria price,delivery,qual]
//	srm-cli expiring [--within N] [--json]   资质到期提醒 (90/30/7 天窗口 + 已过期)
//	srm-cli risk [id] [--json]               空壳特征检测：无 id 列审核队列，带 id 看该供应商信号
//	srm-cli review <id> [--dismiss] [--by user] [--note ...]
//	                                         人工审核闭环：标记已核验(默认)或误报忽略，清出审核队列
//	srm-cli blacklist <id> [--reason ...] / srm-cli unblacklist <id>
//	                                         黑名单（淘汰/禁用）：列入仍可搜到但醒目标记，移出恢复在库
//	srm-cli visibility [--enforce] [--max-level N] [--buffer-days N] [--json]
//	                                         可见性策略收紧：扫描/执行待调整→缓冲→超时自动降级处置
//	srm-cli appeal <id> [--note ...]         录入者申诉待调整标记（暂停倒计时）
//	srm-cli resolve-appeal <id> --grant|--deny [--max-level N]
//	                                         管理员裁决申诉（成立=例外保留 / 驳回=立即降级）
//
// Shared [filters]: --province --city --district --category --min-qual
// --min-rating --owner --q.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/supplider/supplider/backend/internal/domain"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "add":
		err = cmdAdd(args)
	case "list":
		err = cmdList(args)
	case "info":
		err = cmdInfo(args)
	case "search":
		// For now search == list with a keyword; the FTS-backed search
		// port lands next without changing this command's surface.
		err = cmdSearch(args)
	case "export":
		err = cmdExport(args)
	case "compare":
		err = cmdCompare(args)
	case "expiring":
		err = cmdExpiring(args)
	case "risk":
		err = cmdRisk(args)
	case "review":
		err = cmdReview(args)
	case "blacklist":
		err = cmdBlacklist(args, true)
	case "unblacklist":
		err = cmdBlacklist(args, false)
	case "duplicates", "dedup":
		err = cmdDuplicates(args)
	case "visibility", "vis":
		err = cmdVisibility(args)
	case "appeal":
		err = cmdAppeal(args, true)
	case "resolve-appeal":
		err = cmdAppeal(args, false)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "srm-cli %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `srm-cli — Supplider supplier management CLI

Usage:
  srm-cli add <file.json|-> [--visibility N] [--owner user]
  srm-cli list [filters] [--limit N] [--cursor CURSOR] [--json]
  srm-cli search <keyword> [filters]
  srm-cli info <id> [--json]
  srm-cli export [--format json|xlsx] [--out file] [filters] [--include-archived]
  srm-cli compare <id> <id>... [--criteria price,delivery,qual]
  srm-cli expiring [--within N] [--json]
                                 资质到期提醒：列出已过期/7 天内/30 天内/90 天内到期的资质
  srm-cli risk [id] [--json]
                                 空壳特征检测：无 id 列出待人工审核的空壳风险供应商；
                                 带 id 显示该供应商的逐条风险信号（纯本地规则，无需 AI）
  srm-cli review <id> [--dismiss] [--by 用户] [--note 备注]
                                 人工审核闭环：默认标记"已核验"，--dismiss 标记"误报忽略"；
                                 审核后清出风险队列，资料再变更会自动重新进入队列
  srm-cli blacklist <id> [--reason 原因]
                                 列入黑名单（淘汰/禁用，仍可搜到但醒目标记）
  srm-cli unblacklist <id>       移出黑名单，恢复在库
  srm-cli duplicates --name 名称 [--credit-code 代码] [--province 省] [--city 市]
                                 录入去重：按信用代码(强)/公司名(疑似)查重，含黑名单/归档
  srm-cli visibility [--enforce] [--max-level N] [--buffer-days N] [--json]
                                 可见性策略收紧处置：默认只读扫描不合规记录；
                                 --enforce 执行处置（标记待调整/缓冲7天/超时自动降级）
  srm-cli appeal <id> [--note 申诉理由]
                                 录入者对"待调整"标记申诉（暂停自动降级倒计时）
  srm-cli resolve-appeal <id> --grant|--deny [--max-level N]
                                 管理员裁决申诉：--grant 成立(例外保留等级) / --deny 驳回(立即降级)

Filters (shared by list/search/export):
  --province 省  --city 市  --district 区县  --category 品类(逗号分隔, OR)
  --min-qual 二级  --min-rating 4.0  --owner 用户  --q 关键词

Environment:
  SRM_API_ADDR  API base URL (default http://127.0.0.1:7612)
`)
}

// filterFlags are the shared structured filter flags used by list, search
// and export. Bind them onto a FlagSet with bindFilterFlags, then render
// them as query parameters with apply.
type filterFlags struct {
	province  string
	city      string
	district  string
	category  string
	minQual   string
	owner     string
	keyword   string
	minRating float64
}

func bindFilterFlags(fs *flag.FlagSet) *filterFlags {
	f := &filterFlags{}
	fs.StringVar(&f.province, "province", "", "province filter")
	fs.StringVar(&f.city, "city", "", "city filter")
	fs.StringVar(&f.district, "district", "", "district filter")
	fs.StringVar(&f.category, "category", "", "category filter (comma-separated, OR)")
	fs.StringVar(&f.minQual, "min-qual", "", "minimum qualification level (e.g. 二级)")
	fs.StringVar(&f.owner, "owner", "", "owner id filter")
	fs.StringVar(&f.keyword, "q", "", "keyword")
	fs.Float64Var(&f.minRating, "min-rating", 0, "minimum rating")
	return f
}

// apply encodes the filters onto a query string (empty values are skipped).
func (f *filterFlags) apply(q url.Values) {
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("province", f.province)
	set("city", f.city)
	set("district", f.district)
	set("category", f.category)
	set("min_qual_level", f.minQual)
	set("owner", f.owner)
	set("q", f.keyword)
	if f.minRating > 0 {
		q.Set("min_rating", fmt.Sprintf("%g", f.minRating))
	}
}

func apiBase() string {
	if v := os.Getenv("SRM_API_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:7612"
}

// ---------- add ----------

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	visibility := fs.Int("visibility", domain.VisSelf, "visibility level 0-4")
	owner := fs.String("owner", "local", "owner user id")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("add requires a JSON file path (or '-' for stdin)")
	}

	var raw []byte
	var err error
	if fs.Arg(0) == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	// Accept either a full createRequest body or a bare supplier document.
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("input is not valid JSON: %w", err)
	}
	if _, ok := body["basic_info"]; !ok {
		// Treat the whole file as basic_info if it looks like one.
		body = map[string]any{"basic_info": body}
	}
	body["visibility"] = *visibility
	body["owner"] = *owner
	if _, ok := body["source"]; !ok {
		body["source"] = domain.SourceManual
	}

	payload, _ := json.Marshal(body)
	resp, err := http.Post(apiBase()+"/api/v1/suppliers", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return decodeAPIError(resp)
	}
	var doc domain.Supplier
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	fmt.Printf("added %s  %s\n", doc.ID, doc.BasicInfo.CompanyName)
	return nil
}

// ---------- list / search ----------

// cmdSearch splits leading positionals off as the keyword (`search 杭州
// 市政 --city 杭州`) then delegates to the list path.
func cmdSearch(args []string) error {
	// Leading positionals form the keyword; everything from the first
	// flag onward is passed through to the list flag set.
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	cmdArgs := args[i:]
	if kw := strings.Join(args[:i], " "); kw != "" {
		cmdArgs = append([]string{"--q", kw}, cmdArgs...)
	}
	return cmdList(cmdArgs)
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	filters := bindFilterFlags(fs)
	limit := fs.Int("limit", 20, "page size (max 100)")
	cursor := fs.String("cursor", "", "pagination cursor")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// `search <keyword>` positional form.
	if fs.NArg() > 0 && filters.keyword == "" {
		filters.keyword = strings.Join(fs.Args(), " ")
	}

	q := url.Values{}
	filters.apply(q)
	if *cursor != "" {
		q.Set("cursor", *cursor)
	}
	if *limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", *limit))
	}

	resp, err := http.Get(apiBase() + "/api/v1/suppliers?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	var page struct {
		Items      []domain.Summary `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(page)
	}

	if len(page.Items) == 0 {
		fmt.Println("(no suppliers match)")
		return nil
	}
	for _, s := range page.Items {
		loc := strings.TrimSpace(s.Province + " " + s.City + " " + s.District)
		fmt.Printf("%s  %-4.1f★  %-12s  %s  %s\n", s.ID, s.Rating, s.TopQual, loc, s.Name)
	}
	if page.NextCursor != "" {
		fmt.Printf("\n-- more: srm-cli list --cursor %s\n", page.NextCursor)
	}
	return nil
}

// ---------- info ----------

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("info requires a supplier id")
	}
	id := fs.Arg(0)

	resp, err := http.Get(apiBase() + "/api/v1/suppliers/" + url.PathEscape(id))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	raw, _ := io.ReadAll(resp.Body)

	if *asJSON {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err == nil {
			os.Stdout.Write(pretty.Bytes())
			fmt.Println()
			return nil
		}
		fmt.Println(string(raw))
		return nil
	}

	var s domain.Supplier
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	printSupplier(&s)
	return nil
}

func printSupplier(s *domain.Supplier) {
	b := s.BasicInfo
	fmt.Printf("%s  [%s]  %.2f★\n", s.ID, s.Status, s.Rating)
	fmt.Printf("  名称:   %s\n", b.CompanyName)
	if s.Status == "blacklisted" {
		reason := s.BlacklistReason
		if reason == "" {
			reason = "(未填原因)"
		}
		fmt.Printf("  ⚠ 黑名单：%s\n", reason)
	}
	if b.CreditCode != "" {
		fmt.Printf("  信用代码: %s\n", b.CreditCode)
	}
	if b.LegalPerson != "" {
		fmt.Printf("  法人:   %s   注册资本: %s   成立: %s\n", b.LegalPerson, b.RegisteredCapital, b.EstablishmentDate)
	}
	fmt.Printf("  地域:   %s %s %s\n", b.Region.Province, b.Region.City, b.Region.District)
	if b.BusinessScope != "" {
		fmt.Printf("  经营范围: %s\n", b.BusinessScope)
	}
	if len(s.Categories) > 0 {
		fmt.Printf("  品类:   %s\n", strings.Join(s.Categories, " / "))
	}
	for _, q := range s.Qualifications {
		fmt.Printf("  资质:   %s %s (到期 %s, verified=%v)\n", q.Type, q.Level, q.Expiry, q.Verified)
	}
	for _, p := range s.ProductsServices {
		fmt.Printf("  产品/服务: %s %s\n", p.Name, p.UnitPriceRange)
	}
	for _, p := range s.PerformanceHistory {
		fmt.Printf("  绩效:   %s %s  %.1f★ %s\n", p.Date, p.Project, p.Score, p.Feedback)
	}
	if len(s.CustomFields) > 0 {
		fmt.Println("  自定义字段:")
		for k, v := range s.CustomFields {
			fmt.Printf("    %s: %v\n", k, v)
		}
	}
	for _, a := range s.Attachments {
		fmt.Printf("  附件:   %s (%d bytes)\n", a.Name, a.Size)
	}
	fmt.Printf("  更新:   %s\n", s.UpdatedAt.Format(time.RFC3339))
}

// ---------- export ----------

// cmdExport downloads all suppliers matching the shared filters as a JSON
// bundle (full-fidelity backup; default) or an XLSX workbook (exchange).
// With --out the bytes go to a file (format inferred from the extension
// when --format is omitted); otherwise they stream to stdout, so
// `srm-cli export --format xlsx > backup.xlsx` works.
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	filters := bindFilterFlags(fs)
	format := fs.String("format", "", "export format: json (default) or xlsx")
	out := fs.String("out", "", "output file path (default stdout)")
	includeArchived := fs.Bool("include-archived", false, "include archived suppliers")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	fmtType := strings.ToLower(strings.TrimSpace(*format))
	if fmtType == "" {
		// Infer from the output file extension; default to JSON.
		switch strings.ToLower(filepath.Ext(*out)) {
		case ".xlsx":
			fmtType = "xlsx"
		default:
			fmtType = "json"
		}
	}
	if fmtType != "json" && fmtType != "xlsx" {
		return fmt.Errorf("--format must be json or xlsx, got %q", *format)
	}

	q := url.Values{}
	filters.apply(q)
	q.Set("format", fmtType)
	if *includeArchived {
		q.Set("include_archived", "true")
	}

	resp, err := http.Get(apiBase() + "/api/v1/export?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	if *out == "" || *out == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stderr, "exported %s suppliers (%s, %d bytes) → %s\n",
		countExported(fmtType, data), fmtType, len(data), *out)
	return nil
}

// countExported extracts the document count for the human-facing success
// line without parsing xlsx (JSON bundle carries an explicit count; an
// xlsx count is left as "?").
func countExported(format string, data []byte) string {
	if format != "json" {
		return "?"
	}
	var b struct {
		Count int `json:"count"`
	}
	if json.Unmarshal(data, &b) == nil {
		return fmt.Sprintf("%d", b.Count)
	}
	return "?"
}

// reorderFlags moves flag arguments before positional ones. The stdlib
// flag package stops parsing at the first positional argument, but the
// natural CLI order is positional-first for several commands
// (`compare <ids...> --criteria …`, `add <file> --visibility …`,
// `info <id> --json`); reordering lets flags follow positionals.
func reorderFlags(fs *flag.FlagSet, args []string) []string {
	// bool flags take no following value.
	boolFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			boolFlags[f.Name] = true
		}
	})
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			hasValue := strings.Contains(name, "=")
			name = strings.SplitN(name, "=", 2)[0]
			if !hasValue && !boolFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i]) // the flag's value
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return append(flags, positionals...)
}

// ---------- compare ----------

// cmdCompare fetches several suppliers by id and prints a side-by-side
// comparison table across the dimensions that drive selection: 资质,
// 评分/绩效 (交付/质量/配合度) and 价格区间. --criteria trims the rows
// (price,delivery,qual); the default shows every dimension.
func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	criteria := fs.String("criteria", "", "comma-separated dimensions: price,delivery,qual (default all)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("compare requires at least two supplier ids")
	}

	showQual, showDelivery, showPrice := true, true, true
	if strings.TrimSpace(*criteria) != "" {
		showQual, showDelivery, showPrice = false, false, false
		for _, c := range strings.Split(*criteria, ",") {
			switch strings.ToLower(strings.TrimSpace(c)) {
			case "qual", "qualification", "资质":
				showQual = true
			case "delivery", "quality", "cooperation", "绩效":
				showDelivery = true
			case "price", "价格":
				showPrice = true
			default:
				return fmt.Errorf("unknown criteria %q (want price, delivery or qual)", c)
			}
		}
	}

	docs := make([]*domain.Supplier, 0, fs.NArg())
	for _, id := range fs.Args() {
		resp, err := http.Get(apiBase() + "/api/v1/suppliers/" + url.PathEscape(id))
		if err != nil {
			return fmt.Errorf("contact API (is suppliderd running?): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp)
		}
		var doc domain.Supplier
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			return err
		}
		docs = append(docs, &doc)
	}

	// Header row: truncated company name per supplier column.
	header := []string{"维度"}
	for _, d := range docs {
		header = append(header, truncateCell(d.BasicInfo.CompanyName, 22))
	}
	rows := [][]string{header}
	addRow := func(label string, cell func(int) string) {
		row := []string{label}
		for i := range docs {
			row = append(row, truncateCell(cell(i), 22))
		}
		rows = append(rows, row)
	}

	addRow("ID", func(i int) string { return docs[i].ID })
	addRow("地域", func(i int) string {
		r := docs[i].BasicInfo.Region
		return strings.TrimSpace(r.Province + " " + r.City + " " + r.District)
	})
	addRow("品类", func(i int) string { return strings.Join(docs[i].Categories, "/") })
	addRow("联系人", func(i int) string {
		b := docs[i].BasicInfo
		return strings.TrimSpace(b.ContactName + " " + b.ContactPhone)
	})
	if showQual {
		addRow("最高资质", func(i int) string {
			level, _ := domain.TopQualification(docs[i])
			if level == "" {
				return "-"
			}
			return level
		})
	}
	addRow("综合评分", func(i int) string {
		if docs[i].Rating > 0 {
			return fmt.Sprintf("%.2f★", docs[i].Rating)
		}
		return "-"
	})
	if showDelivery {
		addRow("交付均分", func(i int) string { return fmtAvg(docs[i], func(p domain.Performance) float64 { return p.Delivery }) })
		addRow("质量均分", func(i int) string { return fmtAvg(docs[i], func(p domain.Performance) float64 { return p.Quality }) })
		addRow("配合度均分", func(i int) string {
			return fmtAvg(docs[i], func(p domain.Performance) float64 { return p.Cooperation })
		})
		addRow("合作项目数", func(i int) string { return fmt.Sprintf("%d", len(docs[i].PerformanceHistory)) })
	}
	if showPrice {
		addRow("价格区间", func(i int) string {
			parts := make([]string, 0, len(docs[i].ProductsServices))
			for _, p := range docs[i].ProductsServices {
				if p.UnitPriceRange != "" {
					parts = append(parts, p.Name+":"+p.UnitPriceRange)
				}
			}
			if len(parts) == 0 {
				return "-"
			}
			return strings.Join(parts, "; ")
		})
	}
	addRow("状态", func(i int) string { return docs[i].Status })

	printTable(rows)
	return nil
}

// fmtAvg renders the mean of one performance sub-score ("-" when no data).
func fmtAvg(s *domain.Supplier, pick func(domain.Performance) float64) string {
	sum, n := 0.0, 0
	for _, p := range s.PerformanceHistory {
		if v := pick(p); v > 0 {
			sum += v
			n++
		}
	}
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", sum/float64(n))
}

// ---------- expiring (资质到期提醒) ----------

// expiryAlert mirrors supplier.ExpiryAlert JSON.
type expiryAlert struct {
	SupplierID   string `json:"supplier_id"`
	SupplierName string `json:"supplier_name"`
	Province     string `json:"province"`
	City         string `json:"city"`
	QualType     string `json:"qual_type"`
	QualLevel    string `json:"qual_level"`
	CertNo       string `json:"cert_no"`
	Expiry       string `json:"expiry"`
	DaysLeft     int    `json:"days_left"`
	Bucket       string `json:"bucket"`
}

// cmdExpiring lists qualifications already expired or expiring within the
// PRD windows (提前 90/30/7 天), most urgent first. This is the non-AI
// maintenance scan — run it from cron/Task Scheduler for daily reminders.
func cmdExpiring(args []string) error {
	fs := flag.NewFlagSet("expiring", flag.ContinueOnError)
	within := fs.Int("within", 90, "look-ahead window in days (default 90; the 90/30/7 buckets are always labeled)")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("within", fmt.Sprintf("%d", *within))
	resp, err := http.Get(apiBase() + "/api/v1/reminders/expiring?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	var rep struct {
		WithinDays int           `json:"within_days"`
		Count      int           `json:"count"`
		Expired    int           `json:"expired"`
		Items      []expiryAlert `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}

	if rep.Count == 0 {
		fmt.Printf("(no qualifications expiring within %d days)\n", rep.WithinDays)
		return nil
	}

	rows := [][]string{{"状态", "到期日", "剩余", "供应商", "资质", "证书号"}}
	for _, a := range rep.Items {
		rows = append(rows, []string{
			urgencyLabel(a),
			a.Expiry,
			daysLeftLabel(a.DaysLeft),
			truncateCell(a.SupplierName, 24),
			truncateCell(strings.TrimSpace(a.QualType+" "+a.QualLevel), 26),
			a.CertNo,
		})
	}
	printTable(rows)
	fmt.Fprintf(os.Stderr, "\n%d 项资质需要处理（其中 %d 项已过期）。\n", rep.Count, rep.Expired)
	return nil
}

// urgencyLabel renders the bucket as a Chinese status tag.
func urgencyLabel(a expiryAlert) string {
	switch a.Bucket {
	case "expired":
		return "✗ 已过期"
	case "7d":
		return "!! 7 天内"
	case "30d":
		return "! 30 天内"
	default:
		return "90 天内"
	}
}

// daysLeftLabel renders the remaining-time column ("已过期 12 天" /
// "6 天后" / "今天到期").
func daysLeftLabel(days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("已过期 %d 天", -days)
	case days == 0:
		return "今天到期"
	default:
		return fmt.Sprintf("%d 天后", days)
	}
}

// ---------- risk (空壳特征检测, non-AI) ----------

// riskSignal mirrors risk.Signal JSON.
type riskSignal struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// riskReport mirrors risk.Report JSON.
type riskReport struct {
	ShellRisk bool         `json:"shell_risk"`
	Signals   []riskSignal `json:"signals"`
	CheckedAt time.Time    `json:"checked_at"`
}

// shellRiskItem mirrors supplier.ShellRiskItem JSON.
type shellRiskItem struct {
	SupplierID   string       `json:"supplier_id"`
	SupplierName string       `json:"supplier_name"`
	Province     string       `json:"province"`
	City         string       `json:"city"`
	HighCount    int          `json:"high_count"`
	MediumCount  int          `json:"medium_count"`
	Signals      []riskSignal `json:"signals"`
}

// cmdRisk exposes the non-AI shell-company rule engine: with a supplier id it
// prints that supplier's individual signals; without one it lists the
// manual-review queue (active suppliers the local rules flag).
func cmdRisk(args []string) error {
	fs := flag.NewFlagSet("risk", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	// With an id: that supplier's live signal list.
	if fs.NArg() > 0 {
		id := fs.Arg(0)
		resp, err := http.Get(apiBase() + "/api/v1/suppliers/" + url.PathEscape(id) + "/risk")
		if err != nil {
			return fmt.Errorf("contact API (is suppliderd running?): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp)
		}
		var rep riskReport
		if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		if !rep.ShellRisk {
			fmt.Printf("%s  ✓ 未发现空壳风险（本地规则全部通过）\n", id)
			return nil
		}
		fmt.Printf("%s  ⚠ 空壳风险：%d 条信号\n", id, len(rep.Signals))
		for _, sig := range rep.Signals {
			fmt.Printf("  %s  %s\n", riskSeverityTag(sig.Severity), sig.Message)
		}
		return nil
	}

	// No id: the manual-review queue (active, flagged suppliers).
	resp, err := http.Get(apiBase() + "/api/v1/risk/shell")
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var rep struct {
		Count int             `json:"count"`
		Items []shellRiskItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if rep.Count == 0 {
		fmt.Println("(no suppliers flagged for shell-company risk)")
		return nil
	}
	rows := [][]string{{"等级", "高/中", "供应商", "地域", "首要信号"}}
	for _, it := range rep.Items {
		rows = append(rows, []string{
			"⚠ 风险",
			fmt.Sprintf("%d高/%d中", it.HighCount, it.MediumCount),
			truncateCell(it.SupplierName, 24),
			truncateCell(strings.TrimSpace(it.Province+" "+it.City), 12),
			truncateCell(topRiskMessage(it.Signals), 42),
		})
	}
	printTable(rows)
	fmt.Fprintf(os.Stderr, "\n%d 家供应商待人工审核（空壳特征检测，纯本地规则，无需 AI）。\n", rep.Count)
	return nil
}

// topRiskMessage returns the highest-severity signal's message (high before
// medium before low), falling back to the first signal.
func topRiskMessage(sigs []riskSignal) string {
	for _, want := range []string{"high", "medium", "low"} {
		for _, s := range sigs {
			if s.Severity == want {
				return s.Message
			}
		}
	}
	return ""
}

func riskSeverityTag(sev string) string {
	switch sev {
	case "high":
		return "✗ 高"
	case "medium":
		return "! 中"
	default:
		return "· 低"
	}
}

// ---------- blacklist (黑名单 / 淘汰) ----------

// cmdBlacklist adds (add=true) or removes (add=false) a supplier on the
// blacklist. Blacklisted suppliers stay visible in list/search but are
// badged as do-not-use. Mirrors POST .../blacklist and .../unblacklist.
func cmdBlacklist(args []string, add bool) error {
	fs := flag.NewFlagSet("blacklist", flag.ContinueOnError)
	reason := fs.String("reason", "", "blacklist reason (e.g. 资质造假/严重违约)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("blacklist requires a supplier id")
	}
	id := fs.Arg(0)
	path := "/unblacklist"
	var body io.Reader
	if add {
		path = "/blacklist"
		b, _ := json.Marshal(map[string]string{"reason": *reason})
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader([]byte("{}"))
	}
	resp, err := http.Post(
		apiBase()+"/api/v1/suppliers/"+url.PathEscape(id)+path,
		"application/json", body)
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var doc domain.Supplier
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	if add {
		fmt.Printf("blacklisted %s  %s → 已列入黑名单（淘汰/禁用）。\n", doc.ID, doc.BasicInfo.CompanyName)
		if doc.BlacklistReason != "" {
			fmt.Printf("  原因: %s\n", doc.BlacklistReason)
		}
	} else {
		fmt.Printf("unblacklisted %s  %s → 已移出黑名单，恢复在库。\n", doc.ID, doc.BasicInfo.CompanyName)
	}
	return nil
}

// ---------- duplicates (录入去重) ----------

// duplicateMatch mirrors supplier.DuplicateMatch JSON.
type duplicateMatch struct {
	SupplierID string `json:"supplier_id"`
	Name       string `json:"name"`
	Province   string `json:"province"`
	City       string `json:"city"`
	Status     string `json:"status"`
	Level      string `json:"level"`
	Reason     string `json:"reason"`
}

// cmdDuplicates runs the pre-entry duplicate check (录入去重): it reports
// existing suppliers that look like the same company — strong on identical
// credit code, probable on normalized name — including blacklisted/archived
// records so a re-onboarded fraudster is caught. Non-blocking: the user
// decides whether to proceed.
func cmdDuplicates(args []string) error {
	fs := flag.NewFlagSet("duplicates", flag.ContinueOnError)
	name := fs.String("name", "", "company name to check")
	code := fs.String("credit-code", "", "unified social credit code to check")
	prov := fs.String("province", "", "province")
	city := fs.String("city", "", "city")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" && strings.TrimSpace(*code) == "" {
		return fmt.Errorf("duplicates requires --name and/or --credit-code")
	}

	q := url.Values{}
	q.Set("name", *name)
	q.Set("credit_code", *code)
	q.Set("province", *prov)
	q.Set("city", *city)
	resp, err := http.Get(apiBase() + "/api/v1/suppliers/duplicates?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var rep struct {
		Count   int              `json:"count"`
		Matches []duplicateMatch `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if rep.Count == 0 {
		fmt.Println("(no matching suppliers found — likely not a duplicate)")
		return nil
	}
	rows := [][]string{{"强度", "状态", "供应商", "地域", "原因"}}
	for _, m := range rep.Matches {
		level := "疑似"
		if m.Level == "strong" {
			level = "确凿"
		}
		rows = append(rows, []string{
			level,
			statusLabel(m.Status),
			truncateCell(m.Name, 24),
			truncateCell(strings.TrimSpace(m.Province+" "+m.City), 12),
			truncateCell(m.Reason, 40),
		})
	}
	printTable(rows)
	fmt.Fprintf(os.Stderr, "\n发现 %d 家可能重复的供应商%s。录入不会被阻断，请核对后决定是否继续。\n",
		rep.Count, blacklistedHint(rep.Matches))
	return nil
}

func statusLabel(status string) string {
	switch status {
	case "blacklisted":
		return "🚫 黑名单"
	case "archived":
		return "已归档"
	default:
		return "在库"
	}
}

func blacklistedHint(ms []duplicateMatch) string {
	for _, m := range ms {
		if m.Status == "blacklisted" {
			return "（⚠ 含黑名单供应商，请勿重复录入/合作！）"
		}
	}
	return ""
}

// ---------- review (人工审核闭环) ----------

// cmdReview resolves a flagged supplier after a human has inspected it:
// default outcome "verified" (已核验, papers checked), --dismiss marks a
// false positive (误报忽略). Either way the supplier leaves the shell-risk
// queue until a risk-relevant edit reopens it. Mirrors POST .../risk-review.
func cmdReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	dismiss := fs.Bool("dismiss", false, "mark as false positive (误报忽略) instead of verified")
	outcome := fs.String("outcome", "", "explicit outcome: verified | dismissed (overrides --dismiss)")
	by := fs.String("by", "local", "reviewer user id")
	note := fs.String("note", "", "optional review note")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("review requires a supplier id")
	}
	id := fs.Arg(0)

	res := "verified"
	if *dismiss {
		res = "dismissed"
	}
	if strings.TrimSpace(*outcome) != "" {
		res = strings.ToLower(strings.TrimSpace(*outcome))
	}
	if res != "verified" && res != "dismissed" {
		return fmt.Errorf("--outcome must be verified or dismissed, got %q", res)
	}

	body, _ := json.Marshal(map[string]string{
		"outcome": res,
		"by":      *by,
		"note":    *note,
	})
	resp, err := http.Post(
		apiBase()+"/api/v1/suppliers/"+url.PathEscape(id)+"/risk-review",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var doc domain.Supplier
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	label := "已核验（正规供应商）"
	if res == "dismissed" {
		label = "误报忽略"
	}
	fmt.Printf("reviewed %s  %s → %s；已清出风险队列。\n", doc.ID, doc.BasicInfo.CompanyName, label)
	return nil
}

// ---------- visibility policy enforcement (可见性策略收紧) ----------

// visibilityViolation mirrors supplier.VisibilityViolation JSON.
type visibilityViolation struct {
	SupplierID   string     `json:"supplier_id"`
	SupplierName string     `json:"supplier_name"`
	Owner        string     `json:"owner"`
	Province     string     `json:"province"`
	City         string     `json:"city"`
	Visibility   int        `json:"visibility"`
	MaxLevel     int        `json:"max_level"`
	State        string     `json:"state"`
	FlaggedAt    *time.Time `json:"flagged_at,omitempty"`
	Deadline     *time.Time `json:"deadline,omitempty"`
	DaysLeft     int        `json:"days_left,omitempty"`
}

// cmdVisibility drives the visibility-policy disposition flow: by default a
// read-only scan of records above the cap (通知录入者 basis); --enforce runs
// the sweep (flag 待调整 / clear owner-fixed / timeout auto-downgrade).
func cmdVisibility(args []string) error {
	fs := flag.NewFlagSet("visibility", flag.ContinueOnError)
	enforce := fs.Bool("enforce", false, "run the disposition sweep (flag/downgrade) instead of a read-only scan")
	maxLevel := fs.Int("max-level", -1, "policy cap (highest allowed visibility level); default = tier setting")
	bufferDays := fs.Int("buffer-days", 0, "buffer days before auto-downgrade (default 7)")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	if *enforce {
		body := map[string]any{}
		if *maxLevel >= 0 {
			body["max_level"] = *maxLevel
		}
		if *bufferDays > 0 {
			body["buffer_days"] = *bufferDays
		}
		rb, _ := json.Marshal(body)
		resp, err := http.Post(apiBase()+"/api/v1/visibility/enforce", "application/json", bytes.NewReader(rb))
		if err != nil {
			return fmt.Errorf("contact API (is suppliderd running?): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp)
		}
		var rep struct {
			Policy     supplierPolicy        `json:"policy"`
			Flagged    int                   `json:"flagged"`
			Pending    int                   `json:"pending"`
			Appealed   int                   `json:"appealed"`
			Downgraded int                   `json:"downgraded"`
			Resolved   int                   `json:"resolved"`
			Items      []visibilityViolation `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(rep)
		}
		fmt.Printf("可见性处置完成（最高允许等级 %d，缓冲 %d 天）：\n", rep.Policy.MaxLevel, rep.Policy.BufferDays)
		fmt.Printf("  新标记待调整 %d  ｜ 缓冲期中 %d  ｜ 申诉中 %d  ｜ 超时自动降级 %d  ｜ 自行调整已解除 %d\n",
			rep.Flagged, rep.Pending, rep.Appealed, rep.Downgraded, rep.Resolved)
		printVisibilityRows(rep.Items)
		return nil
	}

	// Read-only scan.
	q := url.Values{}
	if *maxLevel >= 0 {
		q.Set("max_level", fmt.Sprintf("%d", *maxLevel))
	}
	if *bufferDays > 0 {
		q.Set("buffer_days", fmt.Sprintf("%d", *bufferDays))
	}
	resp, err := http.Get(apiBase() + "/api/v1/visibility/violations?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var rep struct {
		Policy supplierPolicy        `json:"policy"`
		Count  int                   `json:"count"`
		Items  []visibilityViolation `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if rep.Count == 0 {
		fmt.Printf("(no visibility violations — all records at or below level %d)\n", rep.Policy.MaxLevel)
		return nil
	}
	fmt.Printf("可见性策略：最高允许等级 %d。以下 %d 条记录不合规：\n", rep.Policy.MaxLevel, rep.Count)
	printVisibilityRows(rep.Items)
	fmt.Fprintln(os.Stderr, "\n提示：加 --enforce 执行处置（新违规标记\"待调整\"并给予 7 天缓冲，超时自动降级）。")
	return nil
}

// supplierPolicy mirrors supplier.VisibilityPolicy JSON.
type supplierPolicy struct {
	MaxLevel   int `json:"max_level"`
	BufferDays int `json:"buffer_days"`
}

// printVisibilityRows renders the violation/enforce item list as a CJK-aligned table.
func printVisibilityRows(items []visibilityViolation) {
	if len(items) == 0 {
		return
	}
	rows := [][]string{{"状态", "剩余时间", "截止日", "可见性", "供应商", "地域", "录入人"}}
	for _, v := range items {
		deadline := ""
		if v.Deadline != nil {
			deadline = v.Deadline.Format("2006-01-02")
		}
		rows = append(rows, []string{
			visStateLabel(v.State),
			visDaysLabel(v.State, v.DaysLeft),
			deadline,
			fmt.Sprintf("L%d→L%d", v.Visibility, v.MaxLevel),
			truncateCell(v.SupplierName, 22),
			truncateCell(strings.TrimSpace(v.Province+" "+v.City), 12),
			truncateCell(v.Owner, 10),
		})
	}
	printTable(rows)
}

func visStateLabel(state string) string {
	switch state {
	case "overdue":
		return "✗ 已超期"
	case "appealed":
		return "⏸ 申诉中"
	case "pending":
		return "! 待调整"
	default:
		return "新发现"
	}
}

func visDaysLabel(state string, days int) string {
	switch state {
	case "appealed":
		return "倒计时暂停"
	case "overdue":
		return fmt.Sprintf("已超期 %d 天", -days)
	case "pending":
		if days == 0 {
			return "今天截止"
		}
		return fmt.Sprintf("%d 天后", days)
	default:
		return "-"
	}
}

// cmdAppeal handles both sides of the appeal flow (申诉): file=true is the
// owner filing an appeal (pauses the downgrade countdown); file=false is the
// admin resolving it (--grant = exception, --deny = immediate downgrade).
func cmdAppeal(args []string, file bool) error {
	fs := flag.NewFlagSet("appeal", flag.ContinueOnError)
	note := fs.String("note", "", "appeal reason (filing) or unused on resolve")
	grant := fs.Bool("grant", false, "resolve: 申诉成立 — keep the level as an approved exception")
	deny := fs.Bool("deny", false, "resolve: 驳回 — downgrade to the policy cap immediately")
	maxLevel := fs.Int("max-level", -1, "resolve: policy cap override")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("appeal requires a supplier id")
	}
	id := fs.Arg(0)

	var urlPath string
	var body map[string]any
	if file {
		urlPath = "/appeal-visibility"
		body = map[string]any{"note": *note}
	} else {
		if *grant == *deny { // both set or neither set
			return fmt.Errorf("resolve-appeal requires exactly one of --grant or --deny")
		}
		urlPath = "/resolve-visibility-appeal"
		body = map[string]any{"grant": *grant}
		if *maxLevel >= 0 {
			body["max_level"] = *maxLevel
		}
	}
	rb, _ := json.Marshal(body)
	resp, err := http.Post(
		apiBase()+"/api/v1/suppliers/"+url.PathEscape(id)+urlPath,
		"application/json", bytes.NewReader(rb))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var doc domain.Supplier
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	if file {
		fmt.Printf("appealed %s  %s → 申诉已提交，自动降级倒计时暂停，等待管理员裁决。\n", doc.ID, doc.BasicInfo.CompanyName)
	} else if *grant {
		fmt.Printf("granted %s  %s → 申诉成立：保留可见性等级 L%d（管理员批准的例外）。\n",
			doc.ID, doc.BasicInfo.CompanyName, doc.Visibility)
	} else {
		fmt.Printf("denied %s  %s → 申诉驳回：已立即降级到 L%d。\n",
			doc.ID, doc.BasicInfo.CompanyName, doc.Visibility)
	}
	return nil
}

// printTable prints rows as a padded grid. Widths account for CJK
// characters taking two terminal columns, so Chinese labels stay aligned.
func printTable(rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if w := cellWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	for ri, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-cellWidth(cell)+2))
			}
		}
		fmt.Println(b.String())
		if ri == 0 {
			// Separator under the header, sized by display width.
			total := 0
			for i, w := range widths {
				total += w
				if i < len(widths)-1 {
					total += 2
				}
			}
			fmt.Println(strings.Repeat("-", total))
		}
	}
}

// cellWidth counts a string's terminal width (CJK runes = 2 columns).
func cellWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x2E80 { // CJK + full-width punctuation blocks
			w += 2
		} else {
			w++
		}
	}
	return w
}

// truncateCell cuts a cell to max terminal columns, appending an ellipsis.
func truncateCell(s string, max int) string {
	if cellWidth(s) <= max {
		return s
	}
	w := 0
	var b strings.Builder
	for _, r := range s {
		cw := 1
		if r >= 0x2E80 {
			cw = 2
		}
		if w+cw > max-1 {
			break
		}
		b.WriteRune(r)
		w += cw
	}
	return b.String() + "…"
}

func decodeAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("API %d: %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

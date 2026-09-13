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
//	srm-cli analyze <doc.txt|docx|xlsx|图片|-> [--top N] [--json]
//	                                         AI 文档分析搜索：提取需求 → 语义匹配推荐供应商
//	srm-cli info <id> [--json]
//	srm-cli export [--format json|xlsx] [--out file] [filters] [--include-archived]
//	srm-cli backup [--out file.zip]        download a full library backup (db snapshot + attachments)
//	srm-cli compare <id> <id>... [--criteria price,delivery,qual]
//	srm-cli expiring [--within N] [--json]   资质到期提醒 (90/30/7 天窗口 + 已过期)
//	srm-cli risk [id] [--json]               空壳特征检测：无 id 列审核队列，带 id 看该供应商信号
//	srm-cli review <id> [--dismiss] [--by user] [--note ...]
//	                                         人工审核闭环：标记已核验(默认)或误报忽略，清出审核队列
//	srm-cli blacklist <id> [--reason ...] / srm-cli unblacklist <id>
//	                                         黑名单（淘汰/禁用）：列入仍可搜到但醒目标记，移出恢复在库
//	srm-cli visibility [policy] [--enforce] [--max-level N] [--buffer-days N] [--json]
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
	"mime/multipart"
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
	case "analyze":
		// AI 文档分析搜索 (PRD: srm-cli analyze ./需求.docx): upload a
		// requirement document, LLM extracts the requirement and the semantic
		// index returns ranked supplier recommendations.
		err = cmdAnalyze(args)
	case "export":
		err = cmdExport(args)
	case "backup":
		err = cmdBackup(args)
	case "restore":
		err = cmdRestore(args)
	case "restore-cancel":
		err = cmdRestoreCancel(args)
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
	case "watch":
		err = cmdWatch(args, true)
	case "unwatch":
		err = cmdWatch(args, false)
	case "notifications", "notif":
		err = cmdNotifications(args)
	case "duplicates", "dedup":
		err = cmdDuplicates(args)
	case "merge":
		err = cmdMerge(args)
	case "visibility", "vis":
		err = cmdVisibility(args)
	case "appeal":
		err = cmdAppeal(args, true)
	case "resolve-appeal":
		err = cmdAppeal(args, false)
	case "preference", "pref", "local":
		err = cmdPreference(args)
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
  srm-cli backup [--out file.zip]   下载完整数据备份（数据库快照+全部附件 zip）
  srm-cli restore <备份.zip>        校验并暂存恢复包，完全退出并重启应用后生效（当前数据自动保留回退副本）
  srm-cli restore-cancel            取消已暂存、尚未生效的恢复包
  srm-cli compare <id> <id>... [--criteria price,delivery,qual]
  srm-cli watch <id> / srm-cli unwatch <id>
                                 关注/取消关注：关注后该供应商风险/黑名单/归档/资质临期会进通知
  srm-cli notifications [--unread] [--all-read] [--json]
                                 查看变更通知（铃铛）；--all-read 全部标记已读；list/search 可加 --watched
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
                                 录入去重：按信用代码(确凿)/公司名(疑似)/名称相近(近似，
                                 简称全称、同音字、一字之差；同省才提示)查重，含黑名单/归档
  srm-cli merge <保留id> <并入并归档id>
                                 合并重复供应商：保留前者（身份/基本信息），并入后者的
                                 绩效/附件/资质/品类/产品线/自定义字段后归档后者；含黑名单拒绝
  srm-cli visibility [--enforce] [--max-level N] [--buffer-days N] [--json]
                                 可见性策略收紧处置：默认只读扫描不合规记录；
                                 --enforce 执行处置（标记待调整/缓冲7天/超时自动降级）
  srm-cli visibility policy [--max-level N] [--buffer-days N] [--json]
                                 查看/设置持久化可见性策略（最高可见等级+缓冲天数）；
                                 不带参数=查看，带 --max-level=保存并立即处置一次；
                                 sidecar 开机与每日 24h 自动按该策略处置
  srm-cli appeal <id> [--note 申诉理由]
                                 录入者对"待调整"标记申诉（暂停自动降级倒计时）
  srm-cli resolve-appeal <id> --grant|--deny [--max-level N]
                                 管理员裁决申诉：--grant 成立(例外保留等级) / --deny 驳回(立即降级)
  srm-cli preference [--province 省 [--city 市]] [--clear] [--json]
                                 本地供应商偏好：不带参数=查看，--province/--city 设置（列表与
                                 搜索中本地供应商排最前，仅排序不筛选），--clear 清除

Filters (shared by list/search/export):
  --province 省  --city 市  --district 区县  --category 品类(逗号分隔, OR)
  --min-qual 二级  --min-rating 4.0  --owner 用户  --q 关键词
  --prefer-province 省 [--prefer-city 市]  本次查询本地优先（覆盖已保存偏好）
  --no-local                               本次查询关闭已保存的本地优先排序

Environment:
  SRM_API_ADDR  API base URL (default http://127.0.0.1:7612)
`)
}

// filterFlags are the shared structured filter flags used by list, search
// and export. Bind them onto a FlagSet with bindFilterFlags, then render
// them as query parameters with apply.
type filterFlags struct {
	province       string
	city           string
	district       string
	category       string
	minQual        string
	owner          string
	keyword        string
	minRating      float64
	preferProvince string
	preferCity     string
	noLocal        bool
	watched        bool
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
	// Local-first ranking (本地供应商偏好): explicit hints override the
	// saved home region; --no-local disables the saved preference once.
	fs.StringVar(&f.preferProvince, "prefer-province", "", "rank this province's suppliers first (overrides saved preference)")
	fs.StringVar(&f.preferCity, "prefer-city", "", "rank this city's suppliers first (with --prefer-province)")
	fs.BoolVar(&f.noLocal, "no-local", false, "disable the saved home-region ranking for this query")
	fs.BoolVar(&f.watched, "watched", false, "only followed (关注) suppliers")
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
	set("prefer_province", f.preferProvince)
	set("prefer_city", f.preferCity)
	if f.noLocal {
		q.Set("prefer", "0")
	}
	if f.watched {
		q.Set("watched", "true")
	}
	if f.minRating > 0 {
		q.Set("min_rating", fmt.Sprintf("%g", f.minRating))
	}
}

// localPreference mirrors the HTTP /preferences/local payload.
type localPreference struct {
	Province   string `json:"province"`
	City       string `json:"city"`
	Configured bool   `json:"configured"`
}

// fetchLocalPreference reads the saved home region; failures collapse to
// the empty preference (listing must work even if the endpoint errors).
func fetchLocalPreference() localPreference {
	var p localPreference
	resp, err := http.Get(apiBase() + "/api/v1/preferences/local")
	if err != nil {
		return p
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p
	}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return p
}

// isLocal reports whether a summary row lies in the preferred region.
func isLocal(prov, city string, p localPreference) bool {
	if !p.Configured || p.Province == "" || prov != p.Province {
		return false
	}
	return p.City == "" || city == p.City
}

func apiBase() string {
	if v := os.Getenv("SRM_API_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:7612"
}

// rejectBlankIDs guards against blank positional supplier ids: "" passes a
// NArg count check, but the request then lands on the sidecar's embedded-UI
// SPA fallback (200 text/html) and fails with a confusing
// "invalid character '<'" JSON error. Call after the command's own
// argument-count check; n is how many leading positional args are ids.
func rejectBlankIDs(fs *flag.FlagSet, n int) error {
	for i := 0; i < n; i++ {
		if strings.TrimSpace(fs.Arg(i)) == "" {
			return fmt.Errorf("supplier id must not be blank")
		}
	}
	return nil
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

// analyzeResult is the CLI projection of POST /ai/doc-search.
type analyzeResult struct {
	Requirement struct {
		Requirement  string `json:"requirement"`
		Province     string `json:"province"`
		City         string `json:"city"`
		District     string `json:"district"`
		Category     string `json:"category"`
		MinQualLevel string `json:"min_qual_level"`
	} `json:"requirement"`
	Results []struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Province   string   `json:"province"`
		City       string   `json:"city"`
		District   string   `json:"district"`
		Categories []string `json:"categories"`
		TopQual    string   `json:"top_qual"`
		Rating     float64  `json:"rating"`
		Score      float64  `json:"score"`
		Reason     string   `json:"reason"`
	} `json:"results"`
}

// cmdAnalyze uploads a requirement document to the sidecar's AI doc-search and
// prints the extracted requirement + ranked recommendations. Requires the
// sidecar running AND an AI provider configured (embedding model for the
// vector index). Text / .docx / .xlsx / 图片 supported; PDF → paste text.
func cmdAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	top := fs.Int("top", 10, "max recommendations")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: srm-cli analyze <doc.txt|docx|xlsx|图片|-> [--top N] [--json]")
	}
	path := fs.Arg(0)

	var data []byte
	var name string
	if path == "-" {
		d, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		data, name = d, "stdin.txt"
	} else {
		d, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		data, name = d, filepath.Base(path)
	}
	if len(data) == 0 {
		return fmt.Errorf("document is empty")
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost,
		apiBase()+"/api/v1/ai/doc-search?top_k="+fmt.Sprint(*top), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	var out analyzeResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	r := out.Requirement
	if r.Requirement != "" {
		fmt.Printf("提取需求：%s\n", r.Requirement)
	}
	var tags []string
	for _, t := range []string{r.Province, r.City, r.District, r.Category, r.MinQualLevel} {
		if t != "" {
			tags = append(tags, t)
		}
	}
	if len(tags) > 0 {
		fmt.Printf("维度：%s\n", strings.Join(tags, " · "))
	}
	if len(out.Results) == 0 {
		fmt.Println("(未匹配到供应商；若向量索引为空，请先在应用内「重建索引」)")
		return nil
	}
	fmt.Println()
	fmt.Printf("推荐 %d 家：\n", len(out.Results))
	for _, s := range out.Results {
		loc := strings.TrimSpace(s.Province + " " + s.City + " " + s.District)
		cat := ""
		if len(s.Categories) > 0 {
			cat = " [" + strings.Join(s.Categories, "/") + "]"
		}
		qual := s.TopQual
		if qual != "" {
			qual += "资质"
		}
		fmt.Printf("  %s  %-4.1f★  %-10s  %-8s%s  %s\n  %s\n",
			s.ID, s.Rating, loc, qual, cat, s.Name, s.Reason)
	}
	return nil
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
	// Effective preference for the 📍 marker: explicit flag > saved
	// preference; --no-local shows no marker. Best-effort fetch.
	pref := localPreference{}
	switch {
	case filters.preferProvince != "":
		pref = localPreference{Configured: true, Province: filters.preferProvince, City: filters.preferCity}
	case !filters.noLocal:
		pref = fetchLocalPreference()
	}
	if pref.Configured {
		region := strings.TrimSpace(pref.Province + " " + pref.City)
		fmt.Fprintf(os.Stderr, "本地优先：%s（📍 = 本地供应商，仅排序不筛选）\n", region)
	}
	for _, s := range page.Items {
		loc := strings.TrimSpace(s.Province + " " + s.City + " " + s.District)
		marker := "  "
		if isLocal(s.Province, s.City, pref) {
			marker = "📍"
		}
		fmt.Printf("%s %s  %-4.1f★  %-12s  %s  %s\n", marker, s.ID, s.Rating, s.TopQual, loc, s.Name)
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
	if err := rejectBlankIDs(fs, 1); err != nil {
		return err
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
		// Effective score: explicit overall, else mean of the dimensions
		// set; render the 交付/质量/配合度 breakdown when present.
		score := p.EffectiveScore()
		dims := ""
		if p.Delivery > 0 || p.Quality > 0 || p.Cooperation > 0 {
			parts := make([]string, 0, 3)
			if p.Delivery > 0 {
				parts = append(parts, fmt.Sprintf("交付 %.1f", p.Delivery))
			}
			if p.Quality > 0 {
				parts = append(parts, fmt.Sprintf("质量 %.1f", p.Quality))
			}
			if p.Cooperation > 0 {
				parts = append(parts, fmt.Sprintf("配合度 %.1f", p.Cooperation))
			}
			dims = "  （" + strings.Join(parts, " / ") + "）"
		}
		fmt.Printf("  绩效:   %s %s  %.1f★%s %s\n", p.Date, p.Project, score, dims, p.Feedback)
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
	if fmtType == "json" {
		fmt.Fprintf(os.Stderr, "exported %s suppliers (json, %d bytes) → %s\n",
			jsonCount(data), len(data), *out)
	} else {
		// Row count is not cheaply available without an xlsx parser in the
		// CLI; report the artifact and size rather than an unhelpful "?".
		fmt.Fprintf(os.Stderr, "exported suppliers workbook (xlsx, %d bytes) → %s\n",
			len(data), *out)
	}
	return nil
}

// jsonCount extracts the document count from a full-fidelity JSON export
// bundle for the success message; "?" only if the body is unexpectedly not
// the bundle shape.
func jsonCount(data []byte) string {
	var b struct {
		Count int `json:"count"`
	}
	if json.Unmarshal(data, &b) == nil {
		return fmt.Sprintf("%d", b.Count)
	}
	return "?"
}

// cmdBackup downloads a full library backup (数据备份: consistent DB
// snapshot + all attachments as a zip). Restore: unzip over the data
// directory while the app/sidecar is stopped.
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "output zip path (default supplider-backup-<date>.zip; '-' = stdout)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	resp, err := http.Get(apiBase() + "/api/v1/backup")
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	if *out == "-" {
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			return err
		}
		return nil
	}
	path := *out
	if path == "" {
		path = fmt.Sprintf("supplider-backup-%s.zip", time.Now().UTC().Format("20060102"))
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "backup written: %s (%d bytes).\n", path, n)
	return nil
}

// cmdRestore uploads a backup zip for staged in-app restore (应用内恢复).
// The sidecar validates and stages it; the swap happens at next process
// start, so the printed instruction is to fully quit and reopen the app.
func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: srm-cli restore <备份.zip>")
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequest(http.MethodPost, apiBase()+"/api/v1/backup/restore", f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/zip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return decodeAPIError(resp)
	}
	var st struct {
		Staged   bool `json:"staged"`
		Manifest struct {
			CreatedAt     time.Time `json:"created_at"`
			AttachmentCnt int       `json:"attachment_count"`
		} `json:"manifest"`
		Hint string `json:"hint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	fmt.Printf("恢复包已暂存（备份时间 %s，附件 %d 个）。\n",
		st.Manifest.CreatedAt.Format("2006-01-02 15:04"), st.Manifest.AttachmentCnt)
	fmt.Fprintln(os.Stderr, "请完全退出并重新打开应用（或重启 suppliderd），数据将在启动时恢复；")
	fmt.Fprintln(os.Stderr, "当前数据库会保留为 restore.rollback-* 回退副本。")
	return nil
}

// cmdRestoreCancel discards a staged restore.
func cmdRestoreCancel(_ []string) error {
	req, _ := http.NewRequest(http.MethodDelete, apiBase()+"/api/v1/backup/restore", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	fmt.Println("已取消暂存的恢复包。")
	return nil
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
	if err := rejectBlankIDs(fs, 2); err != nil {
		return err
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
		if err := rejectBlankIDs(fs, 1); err != nil {
			return err
		}
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
	if err := rejectBlankIDs(fs, 1); err != nil {
		return err
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

// ---------- watch / notifications (关注 / 变更通知) ----------

// cmdWatch follows (add=true) or unfollows a supplier.
func cmdWatch(args []string, add bool) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("watch requires a supplier id")
	}
	if err := rejectBlankIDs(fs, 1); err != nil {
		return err
	}
	id := fs.Arg(0)
	b, _ := json.Marshal(map[string]bool{"watched": add})
	req, _ := http.NewRequest(http.MethodPost,
		apiBase()+"/api/v1/suppliers/"+url.PathEscape(id)+"/watch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
		fmt.Printf("watching %s  %s → 已关注（变更将进通知）。\n", doc.ID, doc.BasicInfo.CompanyName)
	} else {
		fmt.Printf("unwatched %s  %s → 已取消关注。\n", doc.ID, doc.BasicInfo.CompanyName)
	}
	return nil
}

// cliNotification mirrors datamodel.Notification.
type cliNotification struct {
	ID           string `json:"id"`
	SupplierID   string `json:"supplier_id"`
	SupplierName string `json:"supplier_name"`
	Type         string `json:"type"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Read         bool   `json:"read"`
	CreatedAt    string `json:"created_at"`
}

type notificationsPayload struct {
	Unread int               `json:"unread"`
	Items  []cliNotification `json:"items"`
}

// cmdNotifications lists the change-notification feed, optionally marks all
// read. --unread shows only unread; --json emits the raw payload.
func cmdNotifications(args []string) error {
	fs := flag.NewFlagSet("notifications", flag.ContinueOnError)
	unreadOnly := fs.Bool("unread", false, "show only unread notifications")
	allRead := fs.Bool("all-read", false, "mark every notification read, then exit")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	if *allRead {
		resp, err := http.Post(apiBase()+"/api/v1/notifications/read-all", "application/json", nil)
		if err != nil {
			return fmt.Errorf("contact API (is suppliderd running?): %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp)
		}
		fmt.Fprintln(os.Stderr, "all notifications marked read.")
		return nil
	}

	q := url.Values{}
	if *unreadOnly {
		q.Set("unread", "true")
	}
	q.Set("limit", "100")
	resp, err := http.Get(apiBase() + "/api/v1/notifications?" + q.Encode())
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var payload notificationsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(payload)
	}
	fmt.Fprintf(os.Stderr, "%d 条未读，共 %d 条通知\n", payload.Unread, len(payload.Items))
	if len(payload.Items) == 0 {
		fmt.Fprintln(os.Stderr, "(暂无通知)")
		return nil
	}
	for _, n := range payload.Items {
		mark := "●"
		if n.Read {
			mark = " "
		}
		ts := n.CreatedAt
		if t, err := time.Parse(time.RFC3339, n.CreatedAt); err == nil {
			ts = t.Local().Format("01-02 15:04")
		}
		fmt.Printf("%s [%s] %s  %s — %s\n", mark, ts, notifSeverityLabel(n.Severity), n.SupplierName, n.Title)
		if n.Body != "" {
			fmt.Printf("    %s\n", n.Body)
		}
	}
	return nil
}

func notifSeverityLabel(s string) string {
	switch s {
	case "danger":
		return "严重"
	case "warning":
		return "警告"
	default:
		return "信息"
	}
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
		level := "近似"
		switch m.Level {
		case "strong":
			level = "确凿"
		case "probable":
			level = "疑似"
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

// ---------- merge (合并重复供应商) ----------

// mergeResult mirrors supplier.MergeResult JSON.
type mergeResult struct {
	MasterID          string `json:"master_id"`
	DuplicateID       string `json:"duplicate_id"`
	PerformanceAdded  int    `json:"performance_added"`
	AttachmentsAdded  int    `json:"attachments_added"`
	QualsAdded        int    `json:"qualifications_added"`
	CategoriesAdded   int    `json:"categories_added"`
	ProductsAdded     int    `json:"products_added"`
	CustomFieldsAdded int    `json:"custom_fields_added"`
}

// cmdMerge consolidates one duplicate into a master: `merge <keep> <archive>`.
// The master keeps its identity and absorbs the duplicate's collections; the
// duplicate is archived (history retained). Blacklisted/archived records are
// refused server-side.
func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("merge requires two supplier ids: merge <保留(主)> <并入并归档(重复)>")
	}
	if err := rejectBlankIDs(fs, 2); err != nil {
		return err
	}
	masterID, dupID := fs.Arg(0), fs.Arg(1)
	body, _ := json.Marshal(map[string]string{"duplicate_id": dupID})
	resp, err := http.Post(
		apiBase()+"/api/v1/suppliers/"+url.PathEscape(masterID)+"/merge",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var out struct {
		Supplier domain.Supplier `json:"supplier"`
		Merged   mergeResult     `json:"merged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	fmt.Printf("merged %s → into %s（%s）\n", out.Merged.DuplicateID, out.Merged.MasterID, out.Supplier.BasicInfo.CompanyName)
	fmt.Printf("  并入：绩效 %d 条、附件 %d 个、资质 %d 项、品类 %d 个、产品线 %d 条、自定义字段 %d 个\n",
		out.Merged.PerformanceAdded, out.Merged.AttachmentsAdded, out.Merged.QualsAdded,
		out.Merged.CategoriesAdded, out.Merged.ProductsAdded, out.Merged.CustomFieldsAdded)
	fmt.Printf("  重复供应商 %s 已归档（历史保留）；保留档案的综合评分 %.1f★。\n",
		out.Merged.DuplicateID, out.Supplier.Rating)
	return nil
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
	if err := rejectBlankIDs(fs, 1); err != nil {
		return err
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
	// `visibility policy` is the admin config subcommand (get/set the
	// persisted cap + buffer); the rest is the scan/enforce report.
	if len(args) > 0 && args[0] == "policy" {
		return cmdVisibilityPolicy(args[1:])
	}
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

// cmdVisibilityPolicy reads or persists the admin visibility policy.
// No flags → GET (show effective policy + whether it is admin-configured
// or the tier default). With --max-level (and optional --buffer-days) →
// persist; the server runs one enforce sweep immediately and reports the
// flagged/downgraded counts. The sidecar afterwards sweeps automatically
// at boot and every 24h using the saved policy.
func cmdVisibilityPolicy(args []string) error {
	fs := flag.NewFlagSet("visibility policy", flag.ContinueOnError)
	maxLevel := fs.Int("max-level", -1, "persist this cap (highest allowed visibility level)")
	bufferDays := fs.Int("buffer-days", 0, "buffer days before auto-downgrade (default 7)")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return err
	}

	if *maxLevel < 0 && *bufferDays == 0 {
		// Read mode.
		resp, err := http.Get(apiBase() + "/api/v1/visibility/policy")
		if err != nil {
			return fmt.Errorf("contact API (is suppliderd running?): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return decodeAPIError(resp)
		}
		var out struct {
			Policy       supplierPolicy `json:"policy"`
			Configured   bool           `json:"configured"`
			TierMaxLevel int            `json:"tier_max_level"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		src := "版本默认（未配置自定义策略）"
		if out.Configured {
			src = "管理员已配置"
		}
		fmt.Printf("可见性策略：最高允许等级 L%d，缓冲期 %d 天（%s；本版本最高可用 L%d）。\n",
			out.Policy.MaxLevel, out.Policy.BufferDays, src, out.TierMaxLevel)
		if !out.Configured {
			fmt.Fprintln(os.Stderr, "提示：用 `srm-cli visibility policy --max-level N` 收紧策略（sidecar 开机会自动处置一次，之后每日一次）。")
		}
		return nil
	}

	// Write mode: max-level is required (buffer alone would be ambiguous
	// since GET vs SET is inferred from flags).
	if *maxLevel < 0 {
		return fmt.Errorf("--max-level is required when setting the policy (0-4)")
	}
	body := map[string]any{"max_level": *maxLevel}
	if *bufferDays > 0 {
		body["buffer_days"] = *bufferDays
	}
	rb, _ := json.Marshal(body)
	resp, err := http.Post(apiBase()+"/api/v1/visibility/policy", "application/json", bytes.NewReader(rb))
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var out struct {
		Policy supplierPolicy `json:"policy"`
		Report struct {
			Flagged    int `json:"flagged"`
			Pending    int `json:"pending"`
			Appealed   int `json:"appealed"`
			Downgraded int `json:"downgraded"`
			Resolved   int `json:"resolved"`
		} `json:"report"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	fmt.Printf("可见性策略已保存：最高允许等级 L%d，缓冲期 %d 天。\n", out.Policy.MaxLevel, out.Policy.BufferDays)
	fmt.Printf("  立即处置：新标记待调整 %d ｜ 缓冲期中 %d ｜ 申诉中 %d ｜ 超时降级 %d ｜ 自行调整解除 %d\n",
		out.Report.Flagged, out.Report.Pending, out.Report.Appealed, out.Report.Downgraded, out.Report.Resolved)
	fmt.Fprintln(os.Stderr, "之后 sidecar 开机时与每日 24h 自动按此策略处置。")
	return nil
}

// supplierPolicy mirrors supplier.VisibilityPolicy JSON.
type supplierPolicy struct {
	MaxLevel   int `json:"max_level"`
	BufferDays int `json:"buffer_days"`
}

// cmdPreference reads, saves or clears the home-region preference
// (本地供应商偏好). No flags = show; --province (optionally --city) saves
// and makes local suppliers rank first in every list/search; --clear
// removes the preference.
func cmdPreference(args []string) error {
	fs := flag.NewFlagSet("preference", flag.ContinueOnError)
	province := fs.String("province", "", "home province (required when setting)")
	city := fs.String("city", "", "home city (optional)")
	clear := fs.Bool("clear", false, "remove the saved preference (local-first off)")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	provinceSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "province" {
			provinceSet = true
		}
	})
	if provinceSet && strings.TrimSpace(*province) == "" {
		return fmt.Errorf("--province must not be empty (use --clear to remove the preference)")
	}

	method, target := http.MethodGet, apiBase()+"/api/v1/preferences/local"
	var body io.Reader
	written := false
	switch {
	case *clear:
		target += "?clear=1"
		method, written = http.MethodPut, true
	case strings.TrimSpace(*province) != "":
		rb, _ := json.Marshal(map[string]string{
			"province": strings.TrimSpace(*province),
			"city":     strings.TrimSpace(*city),
		})
		body = bytes.NewReader(rb)
		method, written = http.MethodPut, true
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact API (is suppliderd running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}
	var p localPreference
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(p)
	}
	if !p.Configured {
		fmt.Println("未配置本地供应商偏好（列表/搜索不做本地优先排序）。")
		if !written {
			fmt.Fprintln(os.Stderr, "设置：srm-cli preference --province 浙江 --city 杭州；清除：srm-cli preference --clear")
		} else {
			fmt.Println("已清除本地供应商偏好。")
		}
		return nil
	}
	region := strings.TrimSpace(p.Province + " " + p.City)
	verb := "当前偏好"
	if written {
		verb = "已保存偏好"
	}
	fmt.Printf("%s：%s。列表与搜索中本地供应商排最前（仅排序，不筛选外地供应商）。\n", verb, region)
	return nil
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
	if err := rejectBlankIDs(fs, 1); err != nil {
		return err
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

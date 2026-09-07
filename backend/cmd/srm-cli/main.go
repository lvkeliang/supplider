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
//	srm-cli list [--city 杭州] [--province 浙江] [--category 施工服务]
//	            [--min-qual 二级] [--min-rating 4.0] [--owner user] [--q 关键词]
//	            [--limit N] [--cursor CURSOR] [--json]
//	srm-cli info <id> [--json]
//
// Roadmap (next loops): search (FTS), export xlsx/json, compare.
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
  srm-cli list [filters] [--json]
  srm-cli info <id> [--json]
  srm-cli search <keyword> [filters]   (FTS-backed in next milestone)

Environment:
  SRM_API_ADDR  API base URL (default http://127.0.0.1:7612)
`)
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
	if err := fs.Parse(args); err != nil {
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
	province := fs.String("province", "", "province filter")
	city := fs.String("city", "", "city filter")
	district := fs.String("district", "", "district filter")
	category := fs.String("category", "", "category filter (comma-separated, OR)")
	minQual := fs.String("min-qual", "", "minimum qualification level (e.g. 二级)")
	minRating := fs.Float64("min-rating", 0, "minimum rating")
	owner := fs.String("owner", "", "owner id filter")
	keyword := fs.String("q", "", "keyword")
	limit := fs.Int("limit", 20, "page size (max 100)")
	cursor := fs.String("cursor", "", "pagination cursor")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// `search <keyword>` positional form.
	if fs.NArg() > 0 && *keyword == "" {
		*keyword = strings.Join(fs.Args(), " ")
	}

	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("province", *province)
	set("city", *city)
	set("district", *district)
	set("category", *category)
	set("min_qual_level", *minQual)
	set("owner", *owner)
	set("q", *keyword)
	set("cursor", *cursor)
	if *minRating > 0 {
		q.Set("min_rating", fmt.Sprintf("%g", *minRating))
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
	if err := fs.Parse(args); err != nil {
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

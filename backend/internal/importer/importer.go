// Package importer turns Excel (.xlsx) files into supplier records.
//
// It is an inbound ADAPTER: it depends on excelize and on the supplier
// service's input types, but the supplier business logic never imports
// excelize. The flow supports MANUAL column mapping first (AI smart-mapping
// is a later, degradable enhancement):
//
//  1. Template()   — download a pre-headered .xlsx template.
//  2. Inspect()    — upload a file; get headers + sample rows + a suggested
//                    column→field mapping (matched by Chinese header aliases;
//                    unrecognized columns default to custom_fields).
//  3. Build()      — apply the (possibly user-edited) mapping to every row,
//                    producing supplier.ImportItem values; empty rows are
//                    skipped and validation happens in the service layer.
//
// 文档式存储: columns that map to no fixed field are kept as free-form
// custom_fields (header = field name), so industry-specific columns
// (垫资能力/品牌/设备…) survive without schema changes.
package importer

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// Field keys for column mapping. FieldCustom keeps the column as a
// custom_field (using its header as the key); FieldIgnore ("") drops it.
const (
	FieldCustom  = "custom"
	FieldIgnore  = ""
	sheetName    = "供应商"
	templateNote = "填写说明：带 * 为必填；品类多个用逗号分隔；未列出的列会作为自定义字段保留。"
)

// fieldSpec describes one mappable fixed field.
type fieldSpec struct {
	Key      string
	Label    string
	Required bool
	Aliases  []string // recognized Chinese header variants (exact, trimmed)
}

// fields is the canonical column set, in template order.
var fields = []fieldSpec{
	{Key: "company_name", Label: "公司名称", Required: true, Aliases: []string{"公司名称", "名称", "供应商名称", "供应商", "企业名称"}},
	{Key: "province", Label: "省份", Required: true, Aliases: []string{"省份", "省", "所在省", "注册地省"}},
	{Key: "city", Label: "城市", Required: true, Aliases: []string{"城市", "市", "所在市", "注册地市"}},
	{Key: "district", Label: "区县", Aliases: []string{"区县", "区", "县", "所在区", "区/县"}},
	{Key: "credit_code", Label: "统一社会信用代码", Aliases: []string{"统一社会信用代码", "信用代码", "社会信用代码"}},
	{Key: "legal_person", Label: "法定代表人", Aliases: []string{"法定代表人", "法人", "法人代表"}},
	{Key: "registered_capital", Label: "注册资本", Aliases: []string{"注册资本", "注册资金"}},
	{Key: "establishment_date", Label: "成立日期", Aliases: []string{"成立日期", "注册日期", "成立时间"}},
	{Key: "business_scope", Label: "经营范围", Aliases: []string{"经营范围"}},
	{Key: "supplier_type", Label: "供应商类型", Aliases: []string{"供应商类型", "类型", "企业类型"}},
	{Key: "contact_name", Label: "联系人", Aliases: []string{"联系人", "联系人姓名"}},
	{Key: "contact_phone", Label: "联系电话", Aliases: []string{"联系电话", "电话", "手机号", "联系方式"}},
	{Key: "contact_email", Label: "联系邮箱", Aliases: []string{"联系邮箱", "邮箱", "电子邮箱"}},
	{Key: "address", Label: "详细地址", Aliases: []string{"详细地址", "地址", "注册地址"}},
	{Key: "categories", Label: "品类", Aliases: []string{"品类", "品类标签", "分类", "类别", "经营品类"}},
	{Key: "qual_type", Label: "资质类型", Aliases: []string{"资质类型", "资质名称", "资质"}},
	{Key: "qual_level", Label: "资质等级", Aliases: []string{"资质等级", "资质级别"}},
	{Key: "score", Label: "综合评分", Aliases: []string{"综合评分", "评分", "总分", "合作评分", "评价分数"}},
	{Key: "delivery", Label: "交付评分", Aliases: []string{"交付评分", "交付分", "交付"}},
	{Key: "quality", Label: "质量评分", Aliases: []string{"质量评分", "质量分", "质量"}},
	{Key: "cooperation", Label: "配合度评分", Aliases: []string{"配合度评分", "配合度分", "配合度", "协作评分", "配合评分"}},
	{Key: "feedback", Label: "合作评价", Aliases: []string{"合作评价", "评价", "项目评价", "评价反馈"}},
}

// FieldOption is the mappable-field list handed to the UI for dropdowns.
type FieldOption struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

// fieldByKey indexes specs by key.
var fieldByKey = func() map[string]fieldSpec {
	m := make(map[string]fieldSpec, len(fields))
	for _, f := range fields {
		m[f.Key] = f
	}
	return m
}()

// aliasIndex maps a normalized header → field key for auto-suggestion.
var aliasIndex = func() map[string]string {
	m := map[string]string{}
	for _, f := range fields {
		for _, a := range f.Aliases {
			m[normalizeHeader(a)] = f.Key
		}
		m[normalizeHeader(f.Label)] = f.Key
	}
	return m
}()

// Defaults applied to every imported row.
type Defaults struct {
	Owner      string `json:"owner"`
	Visibility int    `json:"visibility"`
}

// Inspection is the preview result (no writes).
type Inspection struct {
	Headers       []string      `json:"headers"`
	Sample        [][]string    `json:"sample"`
	Suggested     map[int]string `json:"suggested"` // column index → field key
	Fields        []FieldOption `json:"fields"`
	TotalDataRows int           `json:"total_data_rows"`
}

var splitList = regexp.MustCompile(`[,，、/;；|]+`)

// normalizeHeader trims whitespace and strips a trailing "*" / "：" so
// "公司名称*" and "公司名称：" still match.
func normalizeHeader(h string) string {
	h = strings.TrimSpace(h)
	h = strings.Trim(h, "*＊:： ")
	return h
}

// Template generates the downloadable .xlsx import template.
func Template() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	idx, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return nil, err
	}

	headers := make([]string, 0, len(fields))
	for _, spec := range fields {
		h := spec.Label
		if spec.Required {
			h += "*"
		}
		headers = append(headers, h)
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheetName, cell, h); err != nil {
			return nil, err
		}
	}
	// Example row (row 2) + note (row 3).
	example := map[string]string{
		"company_name": "杭州示例建设有限公司", "province": "浙江", "city": "杭州",
		"district": "西湖区", "legal_person": "张三", "categories": "施工服务,市政工程",
		"qual_type": "建筑工程施工总承包", "qual_level": "二级",
		"score": "4.5", "feedback": "合作顺利，按时交付",
	}
	for c, spec := range fields {
		if v, ok := example[spec.Key]; ok {
			cell, _ := excelize.CoordinatesToCellName(c+1, 2)
			_ = f.SetCellValue(sheetName, cell, v)
		}
	}

	// Instructions live on their OWN sheet so they are never read as a data
	// row on import.
	if _, err := f.NewSheet("填写说明"); err == nil {
		cell, _ := excelize.CoordinatesToCellName(1, 1)
		_ = f.SetCellValue("填写说明", cell, "供应商导入模板填写说明")
		for i, line := range []string{
			templateNote,
			"第一行为表头，请勿修改；第二行为示例，导入前请删除或覆盖。",
			"未在表头中列出的列会作为该供应商的自定义字段保留（如 垫资能力、品牌、设备）。",
			"资质等级支持：特级/一级/二级/三级；品类多个时用逗号分隔。",
			"绩效评分（综合/交付/质量/配合度）取值 0–5，可只填综合分或只填三维（系统按均值计入评分）；每行记一条合作评价。",
		} {
			c, _ := excelize.CoordinatesToCellName(1, i+2)
			_ = f.SetCellValue("填写说明", c, line)
		}
	}
	f.SetActiveSheet(idx) // land on the data sheet for actual editing

	// Bold header row + freeze it.
	style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	_ = f.SetCellStyle(sheetName, "A1", cellName(len(headers), 1), style)
	_ = f.SetPanes(sheetName, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// Inspect reads an uploaded workbook and returns headers, sample rows and
// the suggested column mapping.
func Inspect(data []byte) (*Inspection, error) {
	rows, err := readRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("import: spreadsheet is empty")
	}
	headers := rows[0]
	dataRows := nonEmptyRows(rows[1:])

	suggested := make(map[int]string, len(headers))
	for i, h := range headers {
		trim := strings.TrimSpace(h)
		if trim == "" {
			suggested[i] = FieldIgnore
			continue
		}
		if key, ok := aliasIndex[normalizeHeader(trim)]; ok {
			suggested[i] = key
		} else {
			// Unrecognized column: preserve it as a custom field (文档式).
			suggested[i] = FieldCustom
		}
	}

	sample := dataRows
	if len(sample) > 5 {
		sample = sample[:5]
	}

	opts := make([]FieldOption, 0, len(fields)+2)
	for _, spec := range fields {
		opts = append(opts, FieldOption{Key: spec.Key, Label: spec.Label, Required: spec.Required})
	}

	return &Inspection{
		Headers:       headers,
		Sample:        sample,
		Suggested:     suggested,
		Fields:        opts,
		TotalDataRows: len(dataRows),
	}, nil
}

// Build applies mapping (column index → field key) to every data row and
// returns import items. Fully-empty rows are skipped; mapping columns set to
// FieldCustom become custom_fields keyed by the header text.
func Build(data []byte, mapping map[int]string, defaults Defaults) ([]supplier.ImportItem, error) {
	rows, err := readRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("import: spreadsheet is empty")
	}
	headers := rows[0]

	items := make([]supplier.ImportItem, 0, len(rows)-1)
	for ri, row := range rows[1:] {
		spreadsheetRow := ri + 2 // header is row 1
		if isRowBlank(row) {
			continue
		}

		in := supplier.CreateInput{
			Owner:      defaults.Owner,
			Visibility: defaults.Visibility,
			Source:     domain.SourceImport,
		}
		custom := map[string]any{}
		var qualType, qualLevel string
		var perf domain.Performance
		var hasPerf bool

		for col, key := range mapping {
			if col >= len(row) || key == FieldIgnore {
				continue
			}
			val := strings.TrimSpace(row[col])
			if val == "" {
				continue
			}
			if key == FieldCustom {
				if h := strings.TrimSpace(headerAt(headers, col)); h != "" {
					custom[normalizeHeader(h)] = val
				}
				continue
			}
			if applyPerfField(&perf, key, val) {
				hasPerf = true
				continue
			}
			applyField(&in, &qualType, &qualLevel, key, val)
		}

		if qualType != "" {
			in.Qualifications = []domain.Qualification{{Type: qualType, Level: qualLevel}}
		}
		if hasPerf {
			// One summary collaboration record per imported supplier row.
			in.Performance = []domain.Performance{perf}
		}
		if len(custom) > 0 {
			in.CustomFields = custom
		}
		items = append(items, supplier.ImportItem{Row: spreadsheetRow, Input: in})
	}
	return items, nil
}

// applyPerfField writes a performance-score cell into the per-row record.
// Returns true when key is a performance column. Scores are parsed 0–5;
// unparseable or out-of-range values are ignored (the service validates).
func applyPerfField(perf *domain.Performance, key, val string) bool {
	switch key {
	case "feedback":
		perf.Feedback = val
		return true
	case "score":
		if v, ok := parseScore(val); ok {
			perf.Score = v
		}
		return true
	case "delivery":
		if v, ok := parseScore(val); ok {
			perf.Delivery = v
		}
		return true
	case "quality":
		if v, ok := parseScore(val); ok {
			perf.Quality = v
		}
		return true
	case "cooperation":
		if v, ok := parseScore(val); ok {
			perf.Cooperation = v
		}
		return true
	}
	return false
}

// parseScore parses a 0–5 score, tolerating surrounding whitespace and a
// trailing "分". Returns (0, false) on non-numeric input.
func parseScore(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "分"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if v < 0 || v > domain.MaxScore {
		return 0, false
	}
	return v, true
}

// applyField writes one mapped cell into the create input.
func applyField(in *supplier.CreateInput, qualType, qualLevel *string, key, val string) {
	b := &in.BasicInfo
	switch key {
	case "company_name":
		b.CompanyName = val
	case "province":
		b.Region.Province = val
	case "city":
		b.Region.City = val
	case "district":
		b.Region.District = val
	case "credit_code":
		b.CreditCode = val
	case "legal_person":
		b.LegalPerson = val
	case "registered_capital":
		b.RegisteredCapital = val
	case "establishment_date":
		b.EstablishmentDate = val
	case "business_scope":
		b.BusinessScope = val
	case "supplier_type":
		b.SupplierType = val
	case "contact_name":
		b.ContactName = val
	case "contact_phone":
		b.ContactPhone = val
	case "contact_email":
		b.ContactEmail = val
	case "address":
		b.Address = val
	case "categories":
		in.Categories = splitCategories(val)
	case "qual_type":
		*qualType = val
	case "qual_level":
		*qualLevel = val
	}
}

func splitCategories(val string) []string {
	parts := splitList.Split(val, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func headerAt(headers []string, col int) string {
	if col < len(headers) {
		return headers[col]
	}
	return ""
}

// readRows opens the first sheet of an xlsx workbook from memory.
func readRows(data []byte) ([][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("import: not a valid .xlsx file: %w", err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	if sheet == "" {
		return nil, fmt.Errorf("import: workbook has no sheets")
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("import: read sheet %q: %w", sheet, err)
	}
	return rows, nil
}

// nonEmptyRows drops fully-blank rows.
func nonEmptyRows(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		if !isRowBlank(r) {
			out = append(out, r)
		}
	}
	return out
}

func isRowBlank(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

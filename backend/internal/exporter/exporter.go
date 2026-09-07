// Package exporter turns supplier documents into portable files:
//
//   - JSON bundles (full-fidelity backup / cross-tier data port — the
//     personal→small-business import format, containing every field
//     including change_log, attachments and custom_fields).
//
//   - XLSX workbooks (human-readable exchange; the exported workbook uses
//     the SAME Chinese headers as the import template, so it round-trips
//     through import preview/commit with the auto-suggested mapping and no
//     manual column work).
//
// It is an outbound ADAPTER: together with internal/importer it is the only
// place allowed to import excelize. Business/HTTP code calls these functions
// but never touches excelize itself.
package exporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/supplider/supplider/backend/internal/domain"
)

const (
	// BundleFormat/Version identify the JSON export container. The
	// small-business tier's backup importer keys on these constants.
	BundleFormat  = "supplider-export"
	BundleVersion = 1

	sheetName = "供应商"
)

// Bundle is the full-fidelity JSON export container. It is also the
// personal→small-business data port: a backup taken on the personal tier
// is restored on a server deployment without conversion.
type Bundle struct {
	Format     string             `json:"format"`
	Version    int                `json:"version"`
	ExportedAt time.Time          `json:"exported_at"`
	Count      int                `json:"count"`
	Suppliers  []*domain.Supplier `json:"suppliers"`
}

// JSON serializes a full-fidelity export bundle (every document field,
// including change_log / attachments / custom_fields).
func JSON(docs []*domain.Supplier, now time.Time) ([]byte, error) {
	b := Bundle{
		Format:     BundleFormat,
		Version:    BundleVersion,
		ExportedAt: now.UTC(),
		Count:      len(docs),
		Suppliers:  docs,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return nil, fmt.Errorf("export: encode json: %w", err)
	}
	return buf.Bytes(), nil
}

// xlsxFixedColumns are the fixed columns, in import-template order. The
// headers use the SAME Chinese labels as importer.Template (and are covered
// by its alias table), so an exported workbook round-trips through
// import preview/commit with a fully auto-suggested mapping.
var xlsxFixedColumns = []string{
	"公司名称", "省份", "城市", "区县", "统一社会信用代码", "法定代表人",
	"注册资本", "成立日期", "经营范围", "供应商类型", "联系人", "联系电话",
	"联系邮箱", "详细地址", "品类", "资质类型", "资质等级",
}

// XLSX renders suppliers as a workbook: fixed columns first (identical to
// the import template), then one column per custom-field key (union across
// all documents, sorted). On re-import those extra columns are kept as
// custom_fields automatically (文档式存储 — no schema migration needed).
//
// Fidelity note: the import template has a single qualification slot
// (qual_type + qual_level), so the workbook carries each supplier's
// HIGHEST-ranked qualification; full-fidelity backup is JSON.
func XLSX(docs []*domain.Supplier) ([]byte, error) {
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

	// Union of custom-field keys across all documents.
	keySet := map[string]bool{}
	for _, s := range docs {
		for k := range s.CustomFields {
			keySet[k] = true
		}
	}
	customKeys := make([]string, 0, len(keySet))
	for k := range keySet {
		customKeys = append(customKeys, k)
	}
	sort.Strings(customKeys)

	headers := make([]string, 0, len(xlsxFixedColumns)+len(customKeys))
	headers = append(headers, xlsxFixedColumns...)
	headers = append(headers, customKeys...)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheetName, cell, h); err != nil {
			return nil, err
		}
	}

	for ri, s := range docs {
		row := fixedCells(s)
		for _, k := range customKeys {
			row = append(row, customCell(s.CustomFields[k]))
		}
		for ci, val := range row {
			if val == "" {
				continue // leave empty cells truly empty
			}
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			if err := f.SetCellValue(sheetName, cell, val); err != nil {
				return nil, err
			}
		}
	}

	// Bold header row + freeze it (same presentation as the template).
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
		return nil, fmt.Errorf("export: encode xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

// fixedCells renders the fixed (template-compatible) columns for one
// supplier, in xlsxFixedColumns order.
func fixedCells(s *domain.Supplier) []string {
	b := s.BasicInfo
	q := topQualification(s)
	return []string{
		b.CompanyName,
		b.Region.Province,
		b.Region.City,
		b.Region.District,
		b.CreditCode,
		b.LegalPerson,
		b.RegisteredCapital,
		b.EstablishmentDate,
		b.BusinessScope,
		b.SupplierType,
		b.ContactName,
		b.ContactPhone,
		b.ContactEmail,
		b.Address,
		strings.Join(s.Categories, ","),
		q.Type,
		q.Level,
	}
}

// topQualification returns the supplier's highest-ranked qualification;
// ties keep the first one declared. An unranked qualification still
// exports (rank 0 path) so manual data is never silently dropped.
func topQualification(s *domain.Supplier) domain.Qualification {
	bestIdx, bestRank := -1, -1
	for i, q := range s.Qualifications {
		if r := domain.QualRank(q.Level); r > bestRank {
			bestRank, bestIdx = r, i
		}
	}
	if bestIdx >= 0 {
		return s.Qualifications[bestIdx]
	}
	return domain.Qualification{}
}

// customCell formats a custom-field value as a cell string. Arrays join
// with commas; nil renders empty.
func customCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(t, ",")
	default:
		return fmt.Sprint(t)
	}
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

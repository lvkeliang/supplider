package search

// Searchable-document construction. This lives in the engine-agnostic package
// so the SQLite FTS blob, the in-memory matcher and every future adapter index
// EXACTLY the same text — Chinese substring, pinyin and synonym behavior must
// never diverge across tiers.

import (
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/domain"
)

// DocumentText flattens a supplier into one searchable string.
//
// Order: company name, region (province/city/district — included so a
// concatenated 地域+关键词 query matches suppliers located there even when the
// name omits the city), credit code, legal person, business scope, category
// tags, product/service names and custom field keys/values. Index-time synonym
// expansions (商砼 ↔ 混凝土…) are appended last. Matching is case-insensitive
// at query time; this stores the original casing.
func DocumentText(d *domain.Supplier) string {
	var b strings.Builder
	b.WriteString(d.BasicInfo.CompanyName)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.Region.Province)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.Region.City)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.Region.District)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.CreditCode)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.LegalPerson)
	b.WriteString(" ")
	b.WriteString(d.BasicInfo.BusinessScope)
	for _, c := range d.Categories {
		b.WriteString(" ")
		b.WriteString(c)
	}
	for _, p := range d.ProductsServices {
		b.WriteString(" ")
		b.WriteString(p.Name)
	}
	for k, v := range d.CustomFields {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString(" ")
		fmt.Fprintf(&b, "%v", v)
	}
	text := b.String()
	if extras := SynonymExpansions(text); len(extras) > 0 {
		text += " " + strings.Join(extras, " ")
	}
	return text
}

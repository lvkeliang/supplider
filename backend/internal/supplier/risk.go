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
	"sort"

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

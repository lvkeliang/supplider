// Performance evaluation (绩效评价, 使用 phase). A collaboration record may
// carry an overall score and/or the three PRD dimensions — 交付 delivery,
// 质量 quality, 配合度 cooperation. All scores are 0–5 (0 = not set on the
// record; records only count toward the aggregate once they carry a score).
//
// Aggregation rule for the supplier rating: a record contributes its
// explicit overall score when given; otherwise the mean of whichever
// dimensions were set (see domain.Performance.EffectiveScore). This keeps
// older records (overall score only) and dimension-only records meaningful
// in the same average.
package supplier

import (
	"fmt"

	"github.com/supplider/supplider/backend/internal/domain"
)

// validatePerformance rejects out-of-range scores. Scores are 0–5; zero
// means "not set" on optional fields.
func validatePerformance(records []domain.Performance) error {
	check := func(field string, v float64) error {
		if v < 0 || v > domain.MaxScore {
			return fmt.Errorf("supplier: performance %s must be 0–%g, got %.1f", field, domain.MaxScore, v)
		}
		return nil
	}
	for _, p := range records {
		if err := check("score(总评)", p.Score); err != nil {
			return err
		}
		if err := check("delivery(交付)", p.Delivery); err != nil {
			return err
		}
		if err := check("quality(质量)", p.Quality); err != nil {
			return err
		}
		if err := check("cooperation(配合度)", p.Cooperation); err != nil {
			return err
		}
	}
	return nil
}

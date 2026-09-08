package supplier_test

// Tests for three-dimension performance evaluation (绩效评价: 交付/质量/
// 配合度). The aggregate rating counts a record's explicit overall score
// when given, otherwise the mean of the dimensions set; out-of-range scores
// are rejected on both create and update.

import (
	"context"
	"strings"
	"testing"

	"github.com/supplider/supplider/backend/internal/datamodel/memory"
	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/supplier"
)

// A record with no overall score but dimension scores contributes their
// mean; explicit overall scores win. Aggregate rating averages the
// per-record effective scores.
func TestPerformanceDimensionsFeedRating(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	in := namedInput("三维绩效测试公司", "91330100MA27X7001A", "杭州")
	// Record 1: dimensions only — 交付4.0 质量5.0 配合度3.0 → mean 4.0.
	in.Performance = []domain.Performance{
		{Project: "一期", Date: "2025-01", Delivery: 4.0, Quality: 5.0, Cooperation: 3.0},
		// Record 2: overall score wins even though dimensions are absent.
		{Project: "二期", Date: "2025-06", Score: 5.0, Feedback: "优秀"},
	}
	doc, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// (4.0 + 5.0) / 2 = 4.5
	if doc.Rating < 4.49 || doc.Rating > 4.51 {
		t.Errorf("rating should be 4.5 (dim mean 4.0 + overall 5.0), got %.2f", doc.Rating)
	}
	// EffectiveScore on the dimension-only record is the dimension mean.
	if got := doc.PerformanceHistory[0].EffectiveScore(); got < 3.99 || got > 4.01 {
		t.Errorf("dimension-only effective score = %.2f, want 4.0", got)
	}
}

// Dimension-only record with a single dimension uses that value; records
// with no scores at all do not drag the average.
func TestPerformanceScoreEmptyRecordExcluded(t *testing.T) {
	p := domain.Performance{Delivery: 0, Quality: 0, Cooperation: 0, Score: 0}
	if p.EffectiveScore() != 0 {
		t.Errorf("unscored record effective score must be 0, got %v", p.EffectiveScore())
	}
	p2 := domain.Performance{Quality: 4.0}
	if p2.EffectiveScore() != 4.0 {
		t.Errorf("single-dimension effective score must be 4.0, got %v", p2.EffectiveScore())
	}
}

// Out-of-range scores are rejected on create and update.
func TestPerformanceValidationRejectsOutOfRange(t *testing.T) {
	svc := supplier.NewService(memory.New())
	ctx := context.Background()

	bad := namedInput("评分越界公司", "91330100MA27X7002B", "杭州")
	bad.Performance = []domain.Performance{{Project: "X", Delivery: 5.5}}
	if _, err := svc.Create(ctx, bad); err == nil || !strings.Contains(err.Error(), "delivery") {
		t.Errorf("create with delivery=5.5 must be rejected, got %v", err)
	}

	ok := namedInput("评分正常公司", "91330100MA27X7003C", "杭州")
	doc, err := svc.Create(ctx, ok)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	badUpdate := []domain.Performance{{Project: "Y", Score: 6}}
	if _, err := svc.Update(ctx, doc.ID, supplier.UpdateInput{Performance: &badUpdate}); err == nil ||
		!strings.Contains(err.Error(), "score") {
		t.Errorf("update with score=6 must be rejected, got %v", err)
	}

	// Negative values are rejected too; in-range 0–5 passes.
	neg := namedInput("评分负值公司", "91330100MA27X7004D", "杭州")
	neg.Performance = []domain.Performance{{Project: "Z", Cooperation: -1}}
	if _, err := svc.Create(ctx, neg); err == nil {
		t.Errorf("negative cooperation score must be rejected")
	}
}

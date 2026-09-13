package nlsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/supplider/supplider/backend/internal/aigateway"
)

// fakeGateway implements aigateway.Gateway with an injectable completion.
type fakeGateway struct {
	complete func(req aigateway.ChatRequest) (aigateway.ChatResponse, error)
}

func (f *fakeGateway) Enabled() bool { return true }
func (f *fakeGateway) Complete(_ context.Context, req aigateway.ChatRequest) (aigateway.ChatResponse, error) {
	return f.complete(req)
}
func (f *fakeGateway) Embed(context.Context, []string) ([]aigateway.EmbeddingItem, error) {
	return nil, nil
}

func TestParseStd(t *testing.T) {
	gw := &fakeGateway{complete: func(req aigateway.ChatRequest) (aigateway.ChatResponse, error) {
		return aigateway.ChatResponse{Text: `{"province":"浙江","city":"杭州","category":"市政工程","min_qual_level":"二级","min_rating":4.2,"keyword":"商砼"}`}, nil
	}}
	f, err := Parse(context.Background(), gw, "杭州本地二级以上市政商砼供应商")
	if err != nil {
		t.Fatal(err)
	}
	if f.Province != "浙江" || f.City != "杭州" || f.Category != "市政工程" || f.MinQualLevel != "二级" {
		t.Fatalf("filter = %+v", f)
	}
	if f.MinRating != 4.2 || f.Keyword != "商砼" {
		t.Fatalf("rating/keyword = %+v", f)
	}
}

func TestParseRejectsUnknownQualLevel(t *testing.T) {
	gw := &fakeGateway{complete: func(req aigateway.ChatRequest) (aigateway.ChatResponse, error) {
		return aigateway.ChatResponse{Text: `{"min_qual_level":"肆级"}`}, nil
	}}
	_, err := Parse(context.Background(), gw, "肆级资质")
	if !errors.Is(err, ErrUnknownQualLevel) {
		t.Fatalf("err = %v, want ErrUnknownQualLevel", err)
	}
}

func TestParseClampsRating(t *testing.T) {
	gw := &fakeGateway{complete: func(req aigateway.ChatRequest) (aigateway.ChatResponse, error) {
		return aigateway.ChatResponse{Text: `{"min_rating":9}`}, nil
	}}
	f, err := Parse(context.Background(), gw, "五星好评供应商")
	if err != nil {
		t.Fatal(err)
	}
	if f.MinRating != 5 {
		t.Fatalf("rating = %v, want clamped to 5", f.MinRating)
	}
}

func TestParseStripsMarkdownFences(t *testing.T) {
	gw := &fakeGateway{complete: func(req aigateway.ChatRequest) (aigateway.ChatResponse, error) {
		return aigateway.ChatResponse{Text: "\n```json\n{\"province\":\"浙江\"}\n```\n"}, nil
	}}
	f, err := Parse(context.Background(), gw, "浙江的")
	if err != nil {
		t.Fatal(err)
	}
	if f.Province != "浙江" {
		t.Fatalf("filter = %+v", f)
	}
}

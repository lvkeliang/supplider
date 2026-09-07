package search_test

import (
	"reflect"
	"testing"

	"github.com/supplider/supplider/backend/internal/search"
)

func TestPinyin(t *testing.T) {
	full, initials := search.Pinyin("杭州一建")
	if full != "hangzhouyijian" {
		t.Errorf("full = %q, want hangzhouyijian", full)
	}
	if initials != "hzyj" {
		t.Errorf("initials = %q, want hzyj", initials)
	}

	// Non-hanzi text is ignored (no romanization emitted).
	if f, i := search.Pinyin("ABC 123"); f != "" || i != "" {
		t.Errorf("non-Chinese pinyin = %q/%q, want empty", f, i)
	}
}

func TestSplitTerms(t *testing.T) {
	got := search.SplitTerms(`  Hangzhou, 杭州  市政！ "混凝土" `)
	want := []string{"hangzhou", "杭州", "市政", "混凝土"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitTerms = %v, want %v", got, want)
	}
}

func TestSynonymExpansions(t *testing.T) {
	// A document mentioning 商砼 gains the other group members.
	got := search.SynonymExpansions("本公司主营商砼供应")
	want := []string{"商品混凝土", "混凝土", "砼"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expansions = %v, want %v", got, want)
	}
	// No group member present → nothing.
	if e := search.SynonymExpansions("普通贸易"); e != nil {
		t.Errorf("expansions = %v, want nil", e)
	}
	// Member already present is not re-suggested.
	got = search.SynonymExpansions("混凝土与商砼")
	for _, g := range got {
		if g == "混凝土" || g == "商砼" {
			t.Errorf("already-present term returned as expansion: %q in %v", g, got)
		}
	}
}

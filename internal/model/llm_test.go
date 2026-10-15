package model

import (
	"math"
	"testing"
)

func TestLLMPriceTable_FindAndEstimate(t *testing.T) {
	t.Parallel()
	table := NewLLMPriceTable([]LLMPrice{
		{Model: "claude-opus-5", InputUSD: 5, CacheWriteUSD: 6.25, CacheReadUSD: 0.5, OutputUSD: 25},
		{Model: "claude-opus-5-5", InputUSD: 4, CacheWriteUSD: 5, CacheReadUSD: 0.2, OutputUSD: 20},
	})
	cases := []struct {
		model string
		want  string
		ok    bool
	}{
		{"claude-opus-5-5", "claude-opus-5-5", true},
		{"claude-opus-5-5-20260901", "claude-opus-5-5", true}, // датированный снимок — самый длинный префикс
		{"claude-opus-5", "claude-opus-5", true},
		{"claude-opus-5-20260401", "claude-opus-5", true},
		{"claude-opus-55", "", false}, // префикс только до дефиса
		{"opus", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		p, ok := table.Find(c.model)
		if ok != c.ok || p.Model != c.want {
			t.Errorf("Find(%q) = %q, %v; want %q, %v", c.model, p.Model, ok, c.want, c.ok)
		}
	}
	// Цена: 1 000 входных без кэша, 5 000 записи кэша, 20 000 чтения, 500 выхода на Opus 5.5.
	usage := LLMUsage{Input: 1000, CacheWrite: 5000, CacheRead: 20000, Output: 500}
	got, ok := table.Estimate("claude-opus-5-5", usage)
	want := (1000*4 + 5000*5 + 20000*0.2 + 500*20) / 1e6
	if !ok || math.Abs(got-want) > 1e-12 {
		t.Fatalf("Estimate = %v, %v; want %v", got, ok, want)
	}
	if got, ok := table.Estimate("unknown", usage); ok || got != 0 {
		t.Fatalf("unknown model = %v, %v", got, ok)
	}
	if got, ok := NewLLMPriceTable(nil).Estimate("claude-opus-5-5", usage); ok || got != 0 {
		t.Fatalf("empty table = %v, %v", got, ok)
	}
	if usage.TotalIn() != 26000 {
		t.Fatalf("TotalIn = %d", usage.TotalIn())
	}
	run := LLMRun{TokensIn: 26000, TokensCacheWrite: 5000, TokensCacheRead: 20000, TokensOut: 500}
	if run.Usage() != usage {
		t.Fatalf("Usage = %+v", run.Usage())
	}
	if list := table.Prices(); len(list) != 2 || list[0].Model != "claude-opus-5" {
		t.Fatalf("Prices = %+v", list)
	}
}

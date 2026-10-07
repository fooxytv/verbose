package session

import (
	"math"
	"testing"
)

func TestPriceForModel(t *testing.T) {
	tests := []struct {
		model           string
		wantIn, wantOut float64
		wantKnown       bool
	}{
		{"claude-opus-5", 5, 25, true},
		{"claude-opus-4-8", 5, 25, true},
		{"claude-opus-4-5-20251101", 5, 25, true},
		{"claude-sonnet-5", 2, 10, true},
		{"claude-sonnet-4-5-20250929", 3, 15, true},
		{"claude-haiku-4-5", 1, 5, true},
		{"claude-fable-5-1", 10, 50, true},
		{"claude-3-opus-20240229", 15, 75, true},
		{"claude-3-5-sonnet-20241022", 3, 15, true},
		{"CLAUDE-OPUS-5", 5, 25, true},       // case-insensitive
		{"claude-something-7", 5, 25, false}, // unreleased: falls back
		{"", 5, 25, false},                   // unknown: falls back
	}

	for _, tc := range tests {
		got, known := priceFor(tc.model)
		if got.input != tc.wantIn || got.output != tc.wantOut {
			t.Errorf("priceFor(%q) = %v/%v, want %v/%v",
				tc.model, got.input, got.output, tc.wantIn, tc.wantOut)
		}
		if known != tc.wantKnown {
			t.Errorf("priceFor(%q) known = %v, want %v", tc.model, known, tc.wantKnown)
		}
		if PriceKnown(tc.model) != tc.wantKnown {
			t.Errorf("PriceKnown(%q) = %v, want %v", tc.model, !tc.wantKnown, tc.wantKnown)
		}
	}
}

// Opus 5 is priced at $5/$25 per million. A session is charged on four
// separate counters, so the arithmetic is worth pinning down exactly.
func TestEstimateCostUsesModelRates(t *testing.T) {
	info := SessionInfo{
		Model:            "claude-opus-5",
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  1_000_000,
		CacheWriteTokens: 1_000_000,
	}

	// 5 (input) + 25 (output) + 0.5 (read @ 0.1x) + 6.25 (write @ 1.25x)
	want := 36.75
	if got := estimateCost(info); math.Abs(got-want) > 1e-9 {
		t.Errorf("estimateCost = %v, want %v", got, want)
	}
}

// The bug this replaced charged every model at Claude 3 Opus rates, which
// overstated an Opus 5 session roughly threefold.
func TestEstimateCostDiffersByModel(t *testing.T) {
	usage := func(model string) SessionInfo {
		return SessionInfo{Model: model, InputTokens: 100_000, OutputTokens: 50_000}
	}

	opus := estimateCost(usage("claude-opus-5"))
	haiku := estimateCost(usage("claude-haiku-4-5"))
	legacy := estimateCost(usage("claude-3-opus-20240229"))

	if !(haiku < opus && opus < legacy) {
		t.Errorf("expected haiku < opus 5 < opus 3, got %v, %v, %v", haiku, opus, legacy)
	}
	if math.Abs(opus-1.75) > 1e-9 { // 0.5 + 1.25
		t.Errorf("opus 5 cost = %v, want 1.75", opus)
	}
}

func TestEstimateCostZeroUsage(t *testing.T) {
	if got := estimateCost(SessionInfo{Model: "claude-opus-5"}); got != 0 {
		t.Errorf("estimateCost with no tokens = %v, want 0", got)
	}
}

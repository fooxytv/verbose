package session

import "strings"

// Pricing for Claude Code transcripts. Claude Code records token counts but not
// cost, so cost is reconstructed from the model and the usage. OpenCode records
// a real cost per session and never comes through here.

// modelPrice is USD per million input and output tokens.
type modelPrice struct {
	input  float64
	output float64
}

// cacheReadRate and cacheWriteRate are the standard multipliers on the input
// price: a cache read is a tenth of an uncached input token, a cache write is
// 1.25x. Claude Fable 5.1's flat $0.25/MTok cache read is not modelled.
const (
	cacheReadRate  = 0.1
	cacheWriteRate = 1.25
)

// defaultPrice is used for a model id that matches nothing below — a model
// released after this table was written. Opus-tier rates keep the estimate in
// the right order of magnitude; PriceKnown reports that it was a guess.
var defaultPrice = modelPrice{input: 5.0, output: 25.0}

// prices maps a distinctive fragment of a model id to its rate, most specific
// first: the id carries a date suffix ("claude-opus-4-5-20251101") and
// generation-dependent word order ("claude-3-5-sonnet" vs "claude-sonnet-4-5"),
// so a substring match in a fixed order is steadier than exact keys.
var prices = []struct {
	match string
	price modelPrice
}{
	// Fable / Mythos tier
	{"fable", modelPrice{10.0, 50.0}},
	{"mythos", modelPrice{10.0, 50.0}},

	// Opus 4.x and 5 share a tier; Opus 3 was priced far higher.
	{"opus-5", modelPrice{5.0, 25.0}},
	{"opus-4", modelPrice{5.0, 25.0}},
	{"3-opus", modelPrice{15.0, 75.0}},
	{"opus-3", modelPrice{15.0, 75.0}},

	{"sonnet-5", modelPrice{2.0, 10.0}},
	{"sonnet-4", modelPrice{3.0, 15.0}},
	{"3-7-sonnet", modelPrice{3.0, 15.0}},
	{"3-5-sonnet", modelPrice{3.0, 15.0}},

	{"haiku-4", modelPrice{1.0, 5.0}},
	{"3-5-haiku", modelPrice{0.8, 4.0}},
	{"3-haiku", modelPrice{0.25, 1.25}},
}

// priceFor returns the rate for a model id and whether it was recognised.
func priceFor(model string) (modelPrice, bool) {
	id := strings.ToLower(model)
	for _, p := range prices {
		if strings.Contains(id, p.match) {
			return p.price, true
		}
	}
	return defaultPrice, false
}

// PriceKnown reports whether this model's cost came from the price table
// rather than the fallback rate, so a caller can label an estimate it shows.
func PriceKnown(model string) bool {
	_, known := priceFor(model)
	return known
}

// estimateCost reconstructs a session's USD cost from its token counts and the
// model that produced them.
func estimateCost(info SessionInfo) float64 {
	p, _ := priceFor(info.Model)
	const perMillion = 1_000_000.0

	return float64(info.InputTokens)*p.input/perMillion +
		float64(info.OutputTokens)*p.output/perMillion +
		float64(info.CacheReadTokens)*p.input*cacheReadRate/perMillion +
		float64(info.CacheWriteTokens)*p.input*cacheWriteRate/perMillion
}

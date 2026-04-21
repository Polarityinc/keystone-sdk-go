package keystone

import (
	"math"
	"strings"
)

// EstimateCost returns the USD cost of an LLM call given the model name and
// token counts. Matches the Python and TypeScript SDKs bit-for-bit:
//
//   - Exact model-name match preferred; partial substring match fallback
//     handles versioned aliases (e.g. "claude-3-5-sonnet-20241022-beta").
//   - Cache-read tokens are discounted by DefaultCacheDiscount (90%) against
//     input pricing — the standard Anthropic/OpenAI cache behavior.
//   - Unknown models return 0 rather than an error; the SDK prefers to emit
//     a best-effort cost over breaking the caller.
func EstimateCost(model string, inputTokens, outputTokens, cacheTokens int64) float64 {
	pricing, ok := ModelPricing[model]
	if !ok {
		lower := strings.ToLower(model)
		for key, p := range ModelPricing {
			if strings.Contains(lower, key) || strings.HasPrefix(lower, key) {
				pricing = p
				ok = true
				break
			}
		}
	}
	if !ok {
		return 0
	}

	inputCost := float64(inputTokens) * pricing.Input / 1_000_000
	outputCost := float64(outputTokens) * pricing.Output / 1_000_000
	cacheDiscount := 0.0
	if cacheTokens > 0 {
		cacheDiscount = float64(cacheTokens) * pricing.Input * DefaultCacheDiscount / 1_000_000
	}
	total := inputCost + outputCost - cacheDiscount
	// Match the Python rounding behaviour (6-digit fixed point).
	return math.Round(total*1_000_000) / 1_000_000
}

// PricingTable returns the pricing map. Returned copy is safe to mutate by
// callers — the underlying table is not exposed directly.
func PricingTable() map[string]PricingEntry {
	out := make(map[string]PricingEntry, len(ModelPricing))
	for k, v := range ModelPricing {
		out[k] = v
	}
	return out
}

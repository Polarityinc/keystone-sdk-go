package keystone

import (
	"math"
	"testing"
)

func TestEstimateCostExactMatch(t *testing.T) {
	cost := EstimateCost("claude-sonnet-4-6", 1000, 500, 0)
	// 1000 * 3 / 1M + 500 * 15 / 1M = 0.003 + 0.0075 = 0.0105
	if math.Abs(cost-0.0105) > 1e-9 {
		t.Fatalf("expected 0.0105, got %v", cost)
	}
}

func TestEstimateCostCacheDiscount(t *testing.T) {
	cost := EstimateCost("claude-sonnet-4-6", 1000, 500, 100)
	// discount = 100 * 3 * 0.9 / 1M = 0.00027
	// total = 0.0105 - 0.00027 = 0.01023
	if math.Abs(cost-0.01023) > 1e-9 {
		t.Fatalf("expected 0.01023, got %v", cost)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	if cost := EstimateCost("no-such-model", 100, 100, 0); cost != 0 {
		t.Fatalf("unknown model should return 0, got %v", cost)
	}
}

func TestEstimateCostPartialMatch(t *testing.T) {
	// "claude-sonnet-4-6-beta" should hit "claude-sonnet-4-6" via prefix.
	cost := EstimateCost("claude-sonnet-4-6-beta", 1000, 500, 0)
	if cost == 0 {
		t.Fatalf("expected partial match to find a price, got 0")
	}
}

func TestPricingTableSnapshot(t *testing.T) {
	table := PricingTable()
	if len(table) < 80 {
		t.Fatalf("expected 80+ models in pricing table, got %d", len(table))
	}
	// Spot-check a well-known entry.
	if p, ok := table["gpt-4o"]; !ok || p.Input != 2.5 {
		t.Fatalf("gpt-4o pricing unexpected: %+v ok=%v", p, ok)
	}
}

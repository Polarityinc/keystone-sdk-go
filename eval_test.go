package keystone

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// ─── Tiny in-memory scorer used only by these tests ───────────────────────
//
// Mirrors what the Python tests do with @Scorer: a callable wrapped as a
// Scorer that compares scenario.AgentOutput against scenario.Parameters["expected"].

type exactMatchScorer struct {
	BaseScorer
}

func newExactMatchScorer() *exactMatchScorer {
	return &exactMatchScorer{BaseScorer: NewBaseScorer("exact_match", 1.0, false)}
}

func (s *exactMatchScorer) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	exp, _ := scenario.Parameters["expected"].(string)
	actual := strings.TrimSpace(scenario.AgentOutput)
	if exp == "" {
		return nil, nil
	}
	if actual == strings.TrimSpace(exp) {
		return &Score{Name: "exact_match", Score: 1.0, Passed: true}, nil
	}
	return &Score{Name: "exact_match", Score: 0.0, Passed: false}, nil
}

// ─── Tests ────────────────────────────────────────────────────────────────

func TestEvalRunsEachRowAndAggregates(t *testing.T) {
	ctx := context.Background()

	answers := map[string]string{
		"2+2?": "4",
		"3+3?": "6",
		"5+5?": "10",
	}

	result, err := Eval(ctx, "math", EvalConfig{
		Data: []EvalRow{
			{Input: "2+2?", Expected: "4"},
			{Input: "3+3?", Expected: "6"},
			{Input: "5+5?", Expected: "10"},
		},
		Task: func(_ context.Context, in any) (any, error) {
			return answers[in.(string)], nil
		},
		Scores:         []Scorer{newExactMatchScorer()},
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	if result.Name != "math" {
		t.Errorf("Name=%q want %q", result.Name, "math")
	}
	if len(result.Rows) != 3 {
		t.Errorf("Rows=%d want 3", len(result.Rows))
	}
	for _, row := range result.Rows {
		if row.Scores["exact_match"] != 1.0 {
			t.Errorf("expected score 1.0, got %v for row %v", row.Scores, row.Input)
		}
	}
	summary := result.Summary["exact_match"]
	if summary.Mean != 1.0 || summary.P50 != 1.0 || summary.P95 != 1.0 || summary.Count != 3 {
		t.Errorf("Summary=%+v want mean=1 p50=1 p95=1 count=3", summary)
	}
}

func TestEvalCapturesTaskErrorPerRow(t *testing.T) {
	ctx := context.Background()
	result, err := Eval(ctx, "err", EvalConfig{
		Data: []EvalRow{
			{Input: "ok", Expected: "yes"},
			{Input: "bad", Expected: "yes"},
		},
		Task: func(_ context.Context, in any) (any, error) {
			if in.(string) == "bad" {
				return nil, errors.New("boom")
			}
			return "yes", nil
		},
		Scores:         []Scorer{newExactMatchScorer()},
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("Eval err: %v", err)
	}
	if result.Rows[0].Error != "" {
		t.Errorf("Rows[0].Error=%q want empty", result.Rows[0].Error)
	}
	if !strings.Contains(result.Rows[1].Error, "boom") {
		t.Errorf("Rows[1].Error=%q want to contain 'boom'", result.Rows[1].Error)
	}
	if result.Rows[0].Scores["exact_match"] != 1.0 {
		t.Errorf("first row score=%v want 1.0", result.Rows[0].Scores)
	}
}

func TestEvalPercentilesHalfPassing(t *testing.T) {
	ctx := context.Background()
	rows := make([]EvalRow, 10)
	for i := 0; i < 10; i++ {
		exp := "1"
		if i%2 != 0 {
			exp = "9"
		}
		rows[i] = EvalRow{Input: i, Expected: exp}
	}
	result, err := Eval(ctx, "halves", EvalConfig{
		Data: rows,
		Task: func(_ context.Context, _ any) (any, error) {
			return "1", nil
		},
		Scores:         []Scorer{newExactMatchScorer()},
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("Eval err: %v", err)
	}
	s := result.Summary["exact_match"]
	if s.Count != 10 {
		t.Errorf("count=%d want 10", s.Count)
	}
	if s.Mean != 0.5 {
		t.Errorf("mean=%v want 0.5", s.Mean)
	}
	if s.P50 != 0.5 {
		t.Errorf("p50=%v want 0.5", s.P50)
	}
	if s.P95 != 1.0 {
		t.Errorf("p95=%v want 1.0", s.P95)
	}
}

func TestEvalConcurrencyRunsAllRowsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	var calls int64
	rows := make([]EvalRow, 20)
	for i := 0; i < 20; i++ {
		rows[i] = EvalRow{Input: i, Expected: "yes"}
	}
	result, err := Eval(ctx, "conc", EvalConfig{
		Data: rows,
		Task: func(_ context.Context, _ any) (any, error) {
			atomic.AddInt64(&calls, 1)
			return "yes", nil
		},
		Scores:         []Scorer{newExactMatchScorer()},
		MaxConcurrency: 8,
	})
	if err != nil {
		t.Fatalf("Eval err: %v", err)
	}
	if calls != 20 {
		t.Errorf("calls=%d want 20", calls)
	}
	if len(result.Rows) != 20 {
		t.Errorf("Rows=%d want 20", len(result.Rows))
	}
	if result.Summary["exact_match"].Mean != 1.0 {
		t.Errorf("mean=%v want 1", result.Summary["exact_match"].Mean)
	}
}

func TestEvalNoApiKeyNoExperimentID(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KEYSTONE_API_KEY", "")
	result, err := Eval(ctx, "no-key", EvalConfig{
		Data:   []EvalRow{{Input: "a", Expected: "a"}},
		Task:   func(_ context.Context, in any) (any, error) { return in, nil },
		Scores: []Scorer{newExactMatchScorer()},
	})
	if err != nil {
		t.Fatalf("Eval err: %v", err)
	}
	if result.ExperimentID != "" {
		t.Errorf("ExperimentID=%q want empty", result.ExperimentID)
	}
}

func TestEvalAggregateWithEmptyValues(t *testing.T) {
	got := aggregateEval(nil)
	if got.Count != 0 || got.Mean != 0 || got.P50 != 0 || got.P95 != 0 {
		t.Errorf("aggregateEval(nil)=%+v want zero", got)
	}
}

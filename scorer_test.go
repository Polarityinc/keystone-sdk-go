package keystone

import (
	"context"
	"strings"
	"testing"
)

func scenario(output string, params map[string]interface{}) ScenarioResult {
	return ScenarioResult{
		ScenarioID:  "test",
		Status:      "pass",
		AgentOutput: output,
		Parameters:  params,
	}
}

func TestExactMatchCaseInsensitive(t *testing.T) {
	em := NewExactMatch(EMExpected("hello"), EMCaseSensitive(false))
	s, _ := em.ScoreResult(context.Background(), scenario("HELLO  ", nil))
	if s == nil || !s.Passed || s.Score != 1 {
		t.Fatalf("expected pass, got %+v", s)
	}
}

func TestLevenshteinNearMatch(t *testing.T) {
	lv := NewLevenshtein(LVExpected("hello world"), LVThreshold(0.7))
	s, _ := lv.ScoreResult(context.Background(), scenario("hello wrld", nil))
	if s == nil || !s.Passed || s.Score < 0.8 {
		t.Fatalf("expected high similarity, got %+v", s)
	}
}

func TestNumericDiffWithinTolerance(t *testing.T) {
	nd := NewNumericDiff(NDExpected(42), NDTolerance(0.05))
	s, _ := nd.ScoreResult(context.Background(), scenario("the answer is 41", nil))
	if s == nil || !s.Passed {
		t.Fatalf("expected pass, got %+v", s)
	}
}

func TestJSONDiffIdentical(t *testing.T) {
	jd := NewJSONDiff(JDExpected(map[string]interface{}{"a": float64(1), "b": []interface{}{float64(1), float64(2)}}))
	s, _ := jd.ScoreResult(context.Background(), scenario(`{"a":1,"b":[1,2]}`, nil))
	if s == nil || s.Score != 1 || !s.Passed {
		t.Fatalf("expected perfect match, got %+v", s)
	}
}

func TestJSONValidityOkAndBad(t *testing.T) {
	jv := NewJSONValidity()
	s, _ := jv.ScoreResult(context.Background(), scenario(`{"x":1}`, nil))
	if s == nil || !s.Passed {
		t.Fatalf("expected pass, got %+v", s)
	}
	s, _ = jv.ScoreResult(context.Background(), scenario("not json", nil))
	if s == nil || s.Passed {
		t.Fatalf("expected fail, got %+v", s)
	}
}

func TestSemanticListContainsPartial(t *testing.T) {
	slc := NewSemanticListContains(SLCExpected([]string{"alpha", "beta", "gamma"}), SLCThreshold(0.5))
	s, _ := slc.ScoreResult(context.Background(), scenario("alpha and gamma", nil))
	if s == nil || !s.Passed {
		t.Fatalf("expected pass at 2/3 match, got %+v", s)
	}
}

func TestFileExistsInvariant(t *testing.T) {
	fe := NewFileExists("hello.py", FEGate(true))
	inv := fe.ToInvariant()
	if inv == nil || inv.Check["type"] != "file_exists" || inv.Check["path"] != "hello.py" {
		t.Fatalf("unexpected invariant: %+v", inv)
	}
	if !inv.Gate || inv.Description != "hello.py exists" {
		t.Fatalf("gate/description wrong: %+v", inv)
	}
}

func TestFactualityToRule(t *testing.T) {
	fa := NewFactuality(JudgeModel("gpt-4o"), JudgeTemperature(0.3))
	rule := fa.ToRule()
	if rule == nil || rule.Type != "llm_as_judge" || rule.Name != "factuality" {
		t.Fatalf("bad rule payload: %+v", rule)
	}
	if m, _ := rule.Config["model"].(string); m != "gpt-4o" {
		t.Fatalf("model not threaded through, got %+v", rule.Config)
	}
}

// Mock LLMClient for testing judge scorer flow without hitting paragon-llm-proxy.
type mockLLMClient struct {
	reply string
}

func (m *mockLLMClient) Complete(ctx context.Context, messages []LLMMessage, opts LLMCompleteOpts) (string, error) {
	return m.reply, nil
}

func TestFactualityWithMockClient(t *testing.T) {
	mock := &mockLLMClient{reply: `{"score": 0.9, "passed": true, "reason": "close paraphrase"}`}
	fa := NewFactuality(JudgeClient(mock))
	s, err := fa.ScoreResult(context.Background(), ScenarioResult{
		AgentOutput: "It's Paris",
		Parameters: map[string]interface{}{
			"question": "What is the capital of France?",
			"expected": "Paris",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil || s.Score != 0.9 || !s.Passed {
		t.Fatalf("bad score: %+v", s)
	}
}

func TestCustomScorer(t *testing.T) {
	mine := NewScorer(func(ctx context.Context, s ScenarioResult) (any, error) {
		return strings.Contains(s.AgentOutput, "ok"), nil
	}, CustomName("contains-ok"))
	s, _ := mine.ScoreResult(context.Background(), scenario("all ok", nil))
	if s == nil || !s.Passed || s.Name != "contains-ok" {
		t.Fatalf("bad custom result: %+v", s)
	}
}

func TestScorersToInvariantsFilters(t *testing.T) {
	fe := NewFileExists("hello.py")
	em := NewExactMatch(EMExpected("x")) // has no ToInvariant implementation
	fa := NewFactuality()
	custom := NewScorer(func(ctx context.Context, s ScenarioResult) (any, error) { return true, nil })

	invs := ScorersToInvariants([]Scorer{fe, em, fa, custom})
	// FileExists + Factuality produce invariants; ExactMatch + custom don't.
	if len(invs) != 2 {
		t.Fatalf("expected 2 invariants, got %d: %+v", len(invs), invs)
	}
	if _, ok := invs["file_exists_hello.py"]; !ok {
		t.Fatalf("expected file_exists key, got %v", keysOf(invs))
	}
	if _, ok := invs["factuality"]; !ok {
		t.Fatalf("expected factuality key, got %v", keysOf(invs))
	}
}

func keysOf(m map[string]Invariant) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRunClientScorers(t *testing.T) {
	em := NewExactMatch(EMExpected("hi"), EMCaseSensitive(false))
	lv := NewLevenshtein(LVExpected("hi there"))
	s := scenario("Hi There!", nil)
	scores := RunClientScorers(context.Background(), []Scorer{em, lv}, s)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	// ExactMatch: "Hi There!" vs "hi" normalised → fail
	if scores[0].Passed {
		t.Errorf("ExactMatch should fail: %+v", scores[0])
	}
	// Levenshtein: ratio between "Hi There!" and "hi there" is high
	if scores[1].Score < 0.5 {
		t.Errorf("Levenshtein should be similar: %+v", scores[1])
	}
}

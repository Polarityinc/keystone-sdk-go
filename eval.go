package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// EvalRow is one entry in the eval dataset and (later) one row of EvalResult.
type EvalRow struct {
	// Input passed to Task.
	Input any `json:"input"`
	// Expected value (used by scorers that take an "expected" field).
	Expected any `json:"expected,omitempty"`
	// Output is populated after Task returns. Zero on input.
	Output any `json:"output,omitempty"`
	// Scores per scorer name (populated by Eval).
	Scores map[string]float64 `json:"scores,omitempty"`
	// DurationMs measures the Task call alone.
	DurationMs int64 `json:"duration_ms,omitempty"`
	// Error is non-empty when Task returned an error.
	Error string `json:"error,omitempty"`
}

// EvalSummary holds aggregate stats for a single scorer across all rows.
type EvalSummary struct {
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Count int     `json:"count"`
}

// EvalResult is the value returned from Eval.
type EvalResult struct {
	Name         string                 `json:"name"`
	Rows         []EvalRow              `json:"rows"`
	Summary      map[string]EvalSummary `json:"summary"`
	ExperimentID string                 `json:"experiment_id,omitempty"`
}

// EvalTask is the per-row callable. Receives the row's Input, returns Output.
type EvalTask func(ctx context.Context, input any) (any, error)

// EvalConfig controls one Eval invocation.
type EvalConfig struct {
	Data   []EvalRow
	Task   EvalTask
	Scores []Scorer
	// MaxConcurrency caps parallel Task workers. Default 4.
	MaxConcurrency int
	// Keystone is the optional client used to post results to the dashboard.
	// When nil, we attempt env-based detection (KEYSTONE_API_KEY).
	Keystone *Client
}

// Eval is the Braintrust-parity ergonomic API. Runs Task per row in parallel,
// scores with each Scorer, and returns aggregate stats per scorer.
//
// When KEYSTONE_API_KEY is set (or cfg.Keystone is provided), the eval is
// also reported to the dashboard as a single trace event so it shows up in
// the trace explorer. Otherwise it runs purely locally.
func Eval(ctx context.Context, name string, cfg EvalConfig) (*EvalResult, error) {
	if cfg.Task == nil {
		return nil, fmt.Errorf("keystone: Eval requires a Task")
	}

	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	rows := make([]EvalRow, len(cfg.Data))
	copy(rows, cfg.Data)

	executeRow := func(row *EvalRow) {
		row.Scores = map[string]float64{}
		start := time.Now()
		out, err := cfg.Task(ctx, row.Input)
		row.DurationMs = time.Since(start).Milliseconds()
		if err != nil {
			row.Error = err.Error()
			return
		}
		row.Output = out

		scenario := makeEvalScenario(row.Input, out, row.Expected)
		for _, scorer := range cfg.Scores {
			score := scoreOneEval(ctx, scorer, scenario)
			if score != nil {
				row.Scores[scorer.Name()] = score.Score
			}
		}
	}

	if concurrency <= 1 || len(rows) <= 1 {
		for i := range rows {
			executeRow(&rows[i])
		}
	} else {
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i := range rows {
			i := i
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				executeRow(&rows[i])
			}()
		}
		wg.Wait()
	}

	summary := make(map[string]EvalSummary, len(cfg.Scores))
	for _, scorer := range cfg.Scores {
		var values []float64
		for _, row := range rows {
			if v, ok := row.Scores[scorer.Name()]; ok {
				values = append(values, v)
			}
		}
		summary[scorer.Name()] = aggregateEval(values)
	}

	result := &EvalResult{Name: name, Rows: rows, Summary: summary}

	// Optional dashboard write.
	apiKey := os.Getenv("KEYSTONE_API_KEY")
	if cfg.Keystone != nil && cfg.Keystone.apiKey != "" {
		apiKey = cfg.Keystone.apiKey
	}
	if apiKey != "" {
		if id, err := postEvalToDashboard(ctx, name, result, cfg.Keystone); err == nil {
			result.ExperimentID = id
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Aggregation — kept byte-aligned with the Python and TS implementations.
// ---------------------------------------------------------------------------

func aggregateEval(values []float64) EvalSummary {
	if len(values) == 0 {
		return EvalSummary{}
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return EvalSummary{
		Mean:  sum / float64(len(sorted)),
		P50:   percentile(sorted, 0.5),
		P95:   percentile(sorted, 0.95),
		Count: len(sorted),
	}
}

func percentile(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := pct * float64(len(sorted)-1)
	lo := int(rank)
	hi := lo + 1
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// ---------------------------------------------------------------------------
// Scorer adaptation
// ---------------------------------------------------------------------------

func makeEvalScenario(input, output, expected any) ScenarioResult {
	out := ""
	if output != nil {
		switch v := output.(type) {
		case string:
			out = v
		default:
			b, _ := json.Marshal(output)
			out = string(b)
		}
	}
	params := map[string]any{"input": input}
	if expected != nil {
		params["expected"] = expected
	}
	return ScenarioResult{
		AgentOutput: out,
		Parameters:  params,
		Status:      "pass",
	}
}

func scoreOneEval(ctx context.Context, scorer Scorer, scenario ScenarioResult) *Score {
	// Built-in scorers read scenario.Parameters["expected"] (populated by
	// makeEvalScenario). Custom scorers can read either field directly.
	score, err := scorer.ScoreResult(ctx, scenario)
	if err != nil || score == nil {
		return nil
	}
	return score
}

// ---------------------------------------------------------------------------
// Dashboard reporting
// ---------------------------------------------------------------------------

func postEvalToDashboard(ctx context.Context, name string, result *EvalResult, ks *Client) (string, error) {
	if ks == nil {
		ks = NewClient(Config{})
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	spanID := fmt.Sprintf("span_eval_%x", time.Now().UnixNano())
	totalDur := int64(0)
	for _, row := range result.Rows {
		totalDur += row.DurationMs
	}
	event := map[string]any{
		"ts":          ts,
		"event_type":  "eval",
		"tool":        "Eval:" + name,
		"phase":       "complete",
		"span_id":     spanID,
		"status":      "ok",
		"duration_ms": totalDur,
		"metadata": map[string]any{
			"eval.name":    name,
			"eval.rows":    len(result.Rows),
			"eval.summary": result.Summary,
		},
	}
	if _, err := ks.doJSON(ctx, "POST", "/v1/traces", map[string]any{"events": []any{event}}); err != nil {
		return "", err
	}
	return spanID, nil
}


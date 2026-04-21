package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Mock Keystone server ───────────────────────────────────────────────
//
// An in-memory stand-in for the real Keystone API. Covers every endpoint
// the Go SDK touches, producing deterministic responses so we can exercise
// every public method end-to-end without Supabase / paragon-llm-proxy.
//
// Kept intentionally dumb — tests assert on request shapes, not the
// server's business logic.

type mockServer struct {
	t              *testing.T
	mu             sync.Mutex
	mux            *http.ServeMux
	auth           string
	requests       []requestRecord
	traceBatches   [][]TraceEvent
	experiments    map[string]*RunResults
	runCounts      map[string]*atomic.Int32
	scoreRules     map[string]ScoreRuleInfo
	prompts        map[string]Prompt
	promptVersions map[string]int // slug → latest version
}

type requestRecord struct {
	Method  string
	Path    string
	Query   string
	Headers http.Header
	Body    string
}

func newMockServer(t *testing.T) (*mockServer, *httptest.Server) {
	t.Helper()
	m := &mockServer{
		t:              t,
		mux:            http.NewServeMux(),
		experiments:    make(map[string]*RunResults),
		runCounts:      make(map[string]*atomic.Int32),
		scoreRules:     make(map[string]ScoreRuleInfo),
		prompts:        make(map[string]Prompt),
		promptVersions: make(map[string]int),
	}
	m.registerRoutes()
	ts := httptest.NewServer(m.handler())
	return m, ts
}

// handler wraps mux so every request is recorded.
func (m *mockServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		m.mu.Lock()
		m.requests = append(m.requests, requestRecord{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Headers: r.Header.Clone(), Body: string(body),
		})
		if auth := r.Header.Get("Authorization"); auth != "" {
			m.auth = auth
		}
		m.mu.Unlock()
		m.mux.ServeHTTP(w, r)
	})
}

func (m *mockServer) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func (m *mockServer) registerRoutes() {
	// Sandboxes
	m.mux.HandleFunc("POST /v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 201, Sandbox{
			ID: "sb-1", SpecID: "spec-1", State: "ready", Path: "/tmp/sb-1",
			URL: "http://sb-1", CreatedAt: time.Now(),
			Metadata: map[string]string{"k": "v"},
			Services: map[string]ServiceInfo{"db": {Host: "localhost", Port: 5432, Ready: true}},
		})
	})
	m.mux.HandleFunc("GET /v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []Sandbox{{ID: "sb-1", State: "ready"}})
	})
	m.mux.HandleFunc("GET /v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, Sandbox{ID: r.PathValue("id"), State: "ready"})
	})
	m.mux.HandleFunc("DELETE /v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("POST /v1/sandboxes/{id}/commands", func(w http.ResponseWriter, r *http.Request) {
		var req CommandRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		m.writeJSON(w, 200, CommandResult{
			Command: req.Command, Stdout: "ok\n", ExitCode: 0, DurationMs: 1,
		})
	})
	m.mux.HandleFunc("POST /v1/sandboxes/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("GET /v1/sandboxes/{id}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("hello world"))
	})
	m.mux.HandleFunc("DELETE /v1/sandboxes/{id}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("GET /v1/sandboxes/{id}/state", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, StateSnapshot{SandboxID: r.PathValue("id"), Files: map[string]FileState{}})
	})
	m.mux.HandleFunc("GET /v1/sandboxes/{id}/diff", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, StateDiff{Added: []string{"new.txt"}})
	})
	m.mux.HandleFunc("POST /v1/sandboxes/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Events []TraceEvent `json:"events"` }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		m.mu.Lock()
		m.traceBatches = append(m.traceBatches, payload.Events)
		m.mu.Unlock()
		m.writeJSON(w, 200, map[string]int{"ingested": len(payload.Events)})
	})
	m.mux.HandleFunc("GET /v1/sandboxes/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, TraceResponse{Events: []TraceEvent{{ToolName: "t1"}}, Metrics: &TraceMetrics{TotalToolCalls: 1}})
	})

	// Specs
	m.mux.HandleFunc("POST /v1/specs", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 201, SandboxSpec{ID: "spec-1", Name: "test"})
	})
	m.mux.HandleFunc("GET /v1/specs", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []SandboxSpec{{ID: "spec-1"}})
	})
	m.mux.HandleFunc("GET /v1/specs/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, SandboxSpec{ID: r.PathValue("id")})
	})
	m.mux.HandleFunc("DELETE /v1/specs/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	// Experiments
	m.mux.HandleFunc("POST /v1/experiments", func(w http.ResponseWriter, r *http.Request) {
		var req CreateExperimentRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		m.writeJSON(w, 201, Experiment{ID: "exp-1", Name: req.Name, SpecID: req.SpecID, Status: "created"})
	})
	m.mux.HandleFunc("GET /v1/experiments", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []Experiment{{ID: "exp-1"}})
	})
	m.mux.HandleFunc("POST /v1/experiments/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		m.mu.Lock()
		c, ok := m.runCounts[id]
		if !ok {
			c = &atomic.Int32{}
			m.runCounts[id] = c
		}
		c.Store(0) // reset polling counter on each run
		m.experiments[id] = &RunResults{
			SpecID: "spec-1", ExperimentID: id, TotalScenarios: 2,
			Scenarios: []ScenarioResult{
				{ScenarioID: "s-1", Status: "pass", AgentOutput: "hello world", Parameters: map[string]interface{}{"expected": "hello world"}, Invariants: []InvariantResult{}},
				{ScenarioID: "s-2", Status: "pass", AgentOutput: "bye world", Parameters: map[string]interface{}{"expected": "bye moon"}, Invariants: []InvariantResult{}},
			},
		}
		m.mu.Unlock()
		w.WriteHeader(202)
	})
	m.mux.HandleFunc("GET /v1/experiments/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		m.mu.Lock()
		defer m.mu.Unlock()
		res, ok := m.experiments[id]
		if !ok {
			m.writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		c := m.runCounts[id]
		// Simulate progress: first two polls return 0 done, third returns
		// total done. Lets RunAndWait exercise its polling loop.
		count := c.Add(1)
		done := 0
		if count >= 2 {
			done = res.TotalScenarios
			res.Passed = done
		}
		out := *res
		if count < 2 {
			out.Passed = 0
			out.Failed = 0
			out.Errors = 0
		}
		_ = done
		m.writeJSON(w, 200, out)
	})
	m.mux.HandleFunc("POST /v1/experiments/compare", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, Comparison{BaselineID: "a", CandidateID: "b"})
	})
	m.mux.HandleFunc("GET /v1/metrics/experiments/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, ExperimentMetrics{ExperimentID: r.PathValue("id"), Metrics: map[string]float64{"pass_rate": 1.0}})
	})
	m.mux.HandleFunc("GET /v1/experiments/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format == "ndjson" {
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"kind":"header","experiment_id":"exp-1"}` + "\n"))
			return
		}
		m.writeJSON(w, 200, map[string]any{
			"experiment": []map[string]any{{"id": "exp-1"}},
			"scenarios":  []map[string]any{{"scenario_id": "s-1"}},
		})
	})

	// Export / bulk read — simple one-page responses are enough for coverage.
	writePage := func(w http.ResponseWriter, items []map[string]any, nextCursor string) {
		raw, _ := json.Marshal(items)
		m.writeJSON(w, 200, map[string]any{
			"items":       json.RawMessage(raw),
			"next_cursor": nextCursor,
			"count":       len(items),
		})
	}
	m.mux.HandleFunc("GET /v1/traces", func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		if cursor == "p2" {
			writePage(w, []map[string]any{{"id": float64(3), "tool": "c"}}, "")
			return
		}
		// page 1 returns 2 items + cursor
		writePage(w, []map[string]any{
			{"id": float64(1), "tool": "a", "created_at": "2026-01-01"},
			{"id": float64(2), "tool": "b", "created_at": "2026-01-02"},
		}, "p2")
	})
	m.mux.HandleFunc("GET /v1/traces/{trace_id}", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, map[string]any{"spans": []map[string]any{{"span_id": "s1"}}})
	})
	m.mux.HandleFunc("GET /v1/spans", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, []map[string]any{{"span_id": "s1"}}, "")
	})
	m.mux.HandleFunc("GET /v1/scenarios", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, []map[string]any{{"scenario_id": "s-1"}}, "")
	})
	m.mux.HandleFunc("GET /v1/scores", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, []map[string]any{{"rule_id": "r1", "score": 0.9}}, "")
	})

	// Score rules
	m.mux.HandleFunc("POST /v1/score-rules", func(w http.ResponseWriter, r *http.Request) {
		var req ScoreRuleInfo
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		req.ID = "rule-1"
		m.mu.Lock()
		m.scoreRules[req.ID] = req
		m.mu.Unlock()
		m.writeJSON(w, 201, req)
	})
	m.mux.HandleFunc("GET /v1/score-rules", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []ScoreRuleInfo{{ID: "rule-1", Type: "contains"}})
	})
	m.mux.HandleFunc("DELETE /v1/score-rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("POST /v1/experiments/{id}/score", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 202, map[string]string{"status": "queued"})
	})
	m.mux.HandleFunc("GET /v1/experiments/{id}/scores", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []TraceScoreInfo{{RuleID: "rule-1", Score: 0.9, Passed: true}})
	})

	// Datasets
	m.mux.HandleFunc("POST /v1/datasets", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 201, DatasetInfo{ID: "ds-1", Name: "test", Version: 1})
	})
	m.mux.HandleFunc("GET /v1/datasets", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []DatasetInfo{{ID: "ds-1"}})
	})
	m.mux.HandleFunc("GET /v1/datasets/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, DatasetInfo{ID: r.PathValue("id")})
	})
	m.mux.HandleFunc("DELETE /v1/datasets/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("POST /v1/datasets/{id}/records", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, map[string]int{"added": 2})
	})
	m.mux.HandleFunc("GET /v1/datasets/{id}/records", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []DatasetRecord{{Input: map[string]any{"q": "1"}}})
	})

	// Alerts
	m.mux.HandleFunc("POST /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 201, Alert{ID: "alert-1", Name: "t"})
	})
	m.mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, []Alert{{ID: "alert-1"}})
	})
	m.mux.HandleFunc("DELETE /v1/alerts/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	// Agents — skipped (uses multipart; covered by dedicated test elsewhere).
	m.mux.HandleFunc("GET /v1/agents", func(w http.ResponseWriter, r *http.Request) {
		m.writeJSON(w, 200, AgentPage{Items: []*AgentSnapshot{{ID: "ag-1", Name: "a"}}})
	})

	// Prompts
	m.mux.HandleFunc("POST /v1/prompts", func(w http.ResponseWriter, r *http.Request) {
		var req CreatePromptOpts
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		m.mu.Lock()
		nextVersion := m.promptVersions[req.Slug] + 1
		m.promptVersions[req.Slug] = nextVersion
		p := Prompt{
			ID: fmt.Sprintf("prompt-%d", nextVersion), Slug: req.Slug, Version: nextVersion,
			Tag: req.Tag, Template: req.Template, Variables: req.Variables, Metadata: req.Metadata,
		}
		m.prompts[p.ID] = p
		m.mu.Unlock()
		m.writeJSON(w, 201, p)
	})
	m.mux.HandleFunc("GET /v1/prompts", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		out := make([]Prompt, 0, len(m.prompts))
		for _, p := range m.prompts {
			out = append(out, p)
		}
		m.writeJSON(w, 200, out)
	})
	m.mux.HandleFunc("GET /v1/prompts/{slug}/latest", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		slug := r.PathValue("slug")
		var found *Prompt
		for _, p := range m.prompts {
			p := p
			if p.Slug == slug && (found == nil || p.Version > found.Version) {
				found = &p
			}
		}
		if found == nil {
			m.writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		m.writeJSON(w, 200, found)
	})
	m.mux.HandleFunc("DELETE /v1/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	m.mux.HandleFunc("POST /v1/prompts/{id}/render", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		p, ok := m.prompts[r.PathValue("id")]
		m.mu.Unlock()
		if !ok {
			m.writeJSON(w, 404, nil)
			return
		}
		var req struct {
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		m.writeJSON(w, 200, map[string]string{"rendered": RenderTemplate(p.Template, req.Variables)})
	})
}

// ─── Tests ──────────────────────────────────────────────────────────────

func clientFor(ts *httptest.Server) *Client {
	return NewClient(Config{APIKey: "ks_test", BaseURL: ts.URL, Timeout: 10 * time.Second})
}

func TestSandboxLifecycle(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()

	sb, err := ks.Sandboxes.Create(ctx, CreateSandboxRequest{SpecID: "spec-1"})
	if err != nil || sb.ID != "sb-1" || sb.State != "ready" {
		t.Fatalf("Create: %+v err=%v", sb, err)
	}
	got, err := ks.Sandboxes.Get(ctx, "sb-1")
	if err != nil || got.ID != "sb-1" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	list, err := ks.Sandboxes.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %+v err=%v", list, err)
	}
	cmd, err := ks.Sandboxes.RunCommand(ctx, "sb-1", CommandRequest{Command: "ls"})
	if err != nil || cmd.ExitCode != 0 {
		t.Fatalf("RunCommand: %+v err=%v", cmd, err)
	}
	if err := ks.Sandboxes.WriteFile(ctx, "sb-1", "file.txt", []byte("hi")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	body, err := ks.Sandboxes.ReadFile(ctx, "sb-1", "file.txt")
	if err != nil || string(body) != "hello world" {
		t.Fatalf("ReadFile: got %q err=%v", body, err)
	}
	if err := ks.Sandboxes.DeleteFile(ctx, "sb-1", "file.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := ks.Sandboxes.State(ctx, "sb-1"); err != nil {
		t.Fatalf("State: %v", err)
	}
	if _, err := ks.Sandboxes.Diff(ctx, "sb-1"); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if err := ks.Sandboxes.Destroy(ctx, "sb-1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestTraceIngestAndFetch(t *testing.T) {
	m, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	events := []TraceEvent{
		{Timestamp: time.Now(), EventType: "tool_use", ToolName: "run_cmd", Phase: "invoked", Status: "ok"},
	}
	if err := ks.Sandboxes.IngestTrace(context.Background(), "sb-1", events); err != nil {
		t.Fatalf("Ingest: err=%v", err)
	}
	m.mu.Lock()
	batchCount := len(m.traceBatches)
	m.mu.Unlock()
	if batchCount != 1 {
		t.Fatalf("server received %d batches, want 1", batchCount)
	}
	resp, err := ks.Sandboxes.GetTrace(context.Background(), "sb-1")
	if err != nil || len(resp.Events) != 1 {
		t.Fatalf("GetTrace: %+v err=%v", resp, err)
	}
}

func TestExperimentFlow(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()

	exp, err := ks.Experiments.Create(ctx, CreateExperimentRequest{Name: "e", SpecID: "spec-1"})
	if err != nil || exp.ID != "exp-1" {
		t.Fatalf("Create: %+v err=%v", exp, err)
	}
	if _, err := ks.Experiments.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := ks.Experiments.Run(ctx, "exp-1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := ks.Experiments.Get(ctx, "exp-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := ks.Experiments.Compare(ctx, "a", "b"); err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if _, err := ks.Experiments.Metrics(ctx, "exp-1"); err != nil {
		t.Fatalf("Metrics: %v", err)
	}
}

func TestRunAndWaitWithScores(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()

	scores := []Scorer{
		NewExactMatch(EMExpectedKey("expected"), EMCaseSensitive(false)),
		NewLevenshtein(LVExpectedKey("expected"), LVThreshold(0.5)),
	}
	res, err := ks.Experiments.RunAndWait(ctx, "exp-1", RunAndWaitOpts{
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
		Scores:       scores,
	})
	if err != nil {
		t.Fatalf("RunAndWait: %v", err)
	}
	if res.TotalScenarios != 2 {
		t.Fatalf("expected 2 scenarios, got %d", res.TotalScenarios)
	}
	// Scenario 1 has "hello world" == "hello world" → ExactMatch passes, Levenshtein=1.
	// Scenario 2 has "bye world" vs "bye moon" → ExactMatch fails, Levenshtein ~0.66.
	inv1 := res.Scenarios[0].Invariants
	if len(inv1) != 2 {
		t.Fatalf("scenario 1 expected 2 client-side invariants, got %d", len(inv1))
	}
	if !inv1[0].Passed {
		t.Fatalf("scenario 1 ExactMatch should pass: %+v", inv1[0])
	}
	inv2 := res.Scenarios[1].Invariants
	if inv2[0].Passed {
		t.Fatalf("scenario 2 ExactMatch should fail: %+v", inv2[0])
	}
}

func TestRunAndWaitTimeout(t *testing.T) {
	// Server that never marks the experiment done.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/experiments/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	})
	mux.HandleFunc("GET /v1/experiments/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RunResults{TotalScenarios: 3, Passed: 1})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ks := NewClient(Config{BaseURL: ts.URL})
	_, err := ks.Experiments.RunAndWait(context.Background(), "exp-X", RunAndWaitOpts{
		PollInterval: 10 * time.Millisecond,
		Timeout:      50 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAndWaitCtxCancel(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ks.Experiments.RunAndWait(ctx, "exp-1", RunAndWaitOpts{
		PollInterval: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("expected ctx cancel error")
	}
}

func TestScoringService(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()
	rule, err := ks.Scoring.CreateRule(ctx, "r", "contains", map[string]any{"text": "ok"})
	if err != nil || rule.ID != "rule-1" {
		t.Fatalf("CreateRule: %+v err=%v", rule, err)
	}
	if _, err := ks.Scoring.ListRules(ctx); err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if err := ks.Scoring.ScoreExperiment(ctx, "exp-1", []string{"rule-1"}); err != nil {
		t.Fatalf("ScoreExperiment: %v", err)
	}
	if _, err := ks.Scoring.GetScores(ctx, "exp-1"); err != nil {
		t.Fatalf("GetScores: %v", err)
	}
	if err := ks.Scoring.DeleteRule(ctx, "rule-1"); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
}

func TestPromptService(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()

	p1, err := ks.Prompts.Create(ctx, CreatePromptOpts{
		Slug: "greet", Template: "Hello {name}",
		Variables: map[string]interface{}{"name": "world"},
	})
	if err != nil || p1.Version != 1 {
		t.Fatalf("Create v1: %+v err=%v", p1, err)
	}
	p2, err := ks.Prompts.Create(ctx, CreatePromptOpts{Slug: "greet", Template: "Hi {name}"})
	if err != nil || p2.Version != 2 {
		t.Fatalf("Create v2: %+v err=%v", p2, err)
	}
	latest, err := ks.Prompts.Get(ctx, "greet")
	if err != nil || latest.Version != 2 {
		t.Fatalf("Get latest: %+v err=%v", latest, err)
	}
	rendered, err := ks.Prompts.RenderRemote(ctx, p1.ID, map[string]interface{}{"name": "alex"})
	if err != nil || rendered != "Hello alex" {
		t.Fatalf("RenderRemote: %q err=%v", rendered, err)
	}
	if err := ks.Prompts.Delete(ctx, p1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Local render
	if got := p1.Render(nil); got != "Hello world" {
		t.Fatalf("local default render got %q", got)
	}
	if got := p1.Render(map[string]interface{}{"name": "alex"}); got != "Hello alex" {
		t.Fatalf("local override render got %q", got)
	}
}

func TestExportService(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()

	// Traces paginate through 2 pages → 3 items.
	var rows []map[string]any
	for r := range ks.Export.Traces(ctx, TraceFilter{ExperimentID: "exp-1"}, 2) {
		rows = append(rows, r)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 traces, got %d", len(rows))
	}
	for _, fn := range []func() error{
		func() error { for range ks.Export.Spans(ctx, SpanFilter{TraceID: "t1"}, 10) {}; return nil },
		func() error { for range ks.Export.Scenarios(ctx, ScenarioFilter{ExperimentID: "exp-1"}, 10) {}; return nil },
		func() error { for range ks.Export.Scores(ctx, ScoreFilter{ExperimentID: "exp-1"}, 10) {}; return nil },
	} {
		if err := fn(); err != nil {
			t.Fatalf("export iterator failed: %v", err)
		}
	}
	if _, err := ks.Export.Trace(ctx, "t-root"); err != nil {
		t.Fatalf("Trace: %v", err)
	}
	exp, err := ks.Export.Experiment(ctx, "exp-1", ExportJSON)
	if err != nil || exp == nil {
		t.Fatalf("Experiment JSON: %+v err=%v", exp, err)
	}
	nd, err := ks.Export.Experiment(ctx, "exp-1", ExportNDJSON)
	if err != nil {
		t.Fatalf("Experiment NDJSON: %v", err)
	}
	if s, ok := nd.(string); !ok || !strings.Contains(s, "experiment_id") {
		t.Fatalf("expected ndjson string, got %T %v", nd, nd)
	}
}

func TestExportCancellation(t *testing.T) {
	// Server that streams pages forever.
	mux := http.NewServeMux()
	var calls atomic.Int32
	mux.HandleFunc("GET /v1/traces", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"id": 1, "tool": "x"}},
			"next_cursor": "always",
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ks := NewClient(Config{BaseURL: ts.URL})

	ctx, cancel := context.WithCancel(context.Background())
	ch := ks.Export.Traces(ctx, TraceFilter{}, 10)
	<-ch // pull one
	cancel()
	// Drain; should stop promptly.
	for range ch {
	}
	if calls.Load() < 1 {
		t.Fatalf("expected at least one call, got %d", calls.Load())
	}
}

func TestDatasetService(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()
	if _, err := ks.Datasets.Create(ctx, "d", "desc"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := ks.Datasets.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := ks.Datasets.Get(ctx, "ds-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := ks.Datasets.AddRecords(ctx, "ds-1", []DatasetRecord{{Input: map[string]any{"q": "1"}}}); err != nil {
		t.Fatalf("AddRecords: %v", err)
	}
	if _, err := ks.Datasets.GetRecords(ctx, "ds-1"); err != nil {
		t.Fatalf("GetRecords: %v", err)
	}
	if err := ks.Datasets.Delete(ctx, "ds-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestAlertService(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	ctx := context.Background()
	if _, err := ks.Alerts.Create(ctx, CreateAlertRequest{Name: "a", Condition: "pass_rate<0.8", Notify: "webhook"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := ks.Alerts.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := ks.Alerts.Delete(ctx, "alert-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestAuthHeaderPropagation(t *testing.T) {
	m, ts := newMockServer(t)
	defer ts.Close()
	ks := NewClient(Config{APIKey: "ks_secret_abc", BaseURL: ts.URL})
	_, _ = ks.Sandboxes.List(context.Background())
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.auth != "Bearer ks_secret_abc" {
		t.Fatalf("Authorization header not set correctly: %q", m.auth)
	}
}

func TestAPIErrorParsing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/bad", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"forbidden"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ks := NewClient(Config{BaseURL: ts.URL})
	_, err := ks.Sandboxes.Get(context.Background(), "bad")
	if err == nil {
		t.Fatalf("expected error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 403 || apiErr.Message != "forbidden" {
		t.Fatalf("wrong error: %+v", apiErr)
	}
}

func asAPIError(err error, out **APIError) bool {
	if apiErr, ok := err.(*APIError); ok {
		*out = apiErr
		return true
	}
	return false
}

// ─── Scorer coverage — exercises every constructor + ToInvariant/ToRule ─

func TestEveryScorerConstructs(t *testing.T) {
	mock := &mockLLMClient{reply: `{"score":0.7,"passed":true,"reason":"ok"}`}
	scenarios := []ScenarioResult{
		{AgentOutput: "hello world", Parameters: map[string]interface{}{
			"question":     "what is 2+2?",
			"expected":     "4",
			"answer":       "4",
			"instruction":  "be brief",
			"baseline":     "meh",
			"source":       "a long source doc",
			"context":      "supporting evidence",
			"expected_sql": "SELECT 1",
			"reference":    "bonjour",
		}},
	}

	scorers := []Scorer{
		// Heuristic
		NewExactMatch(EMExpected("hello world")),
		NewLevenshtein(LVExpected("hello world"), LVThreshold(0.5)),
		NewNumericDiff(NDExpected(42)),
		NewJSONDiff(JDExpected(map[string]interface{}{"a": float64(1)})),
		NewJSONValidity(),
		NewSemanticListContains(SLCExpected([]string{"hello"})),
		// Judges
		NewFactuality(JudgeClient(mock)),
		NewBattle(JudgeClient(mock)),
		NewClosedQA(JudgeClient(mock)),
		NewHumor(JudgeClient(mock)),
		NewModeration(JudgeClient(mock)),
		NewSummarization(JudgeClient(mock)),
		NewSQLJudge(JudgeClient(mock)),
		NewTranslation(JudgeClient(mock)),
		NewSecurity(JudgeClient(mock)),
		// RAG
		NewContextPrecision(nil, JudgeClient(mock)),
		NewContextRecall(nil, JudgeClient(mock)),
		NewContextRelevancy(nil, JudgeClient(mock)),
		NewContextEntityRecall(nil, JudgeClient(mock)),
		NewFaithfulness(nil, JudgeClient(mock)),
		NewAnswerRelevancy(nil, JudgeClient(mock)),
		NewAnswerSimilarity(nil, JudgeClient(mock)),
		NewAnswerCorrectness(nil, JudgeClient(mock)),
		// Embedding
		NewEmbeddingSimilarity(func(ctx context.Context, text string) ([]float64, error) {
			return []float64{float64(len(text)), 1}, nil
		}, ESExpected("hello world")),
		// Sandbox
		NewFileExists("out.txt"),
		NewFileContains("out.txt", FCContains("ok")),
		NewCommandExits("echo hi"),
		NewSQLEquals(SQLEqualsOpts{Service: "db", Query: "SELECT 1", Equals: 1}),
		NewLLMJudge("Is this good?"),
	}

	if len(scorers) != 29 {
		// 28 plus the generic LLMJudge alias that maps to the same rule type
		t.Fatalf("expected 29 scorer instances (28 Py/TS + legacy LLMJudge), got %d", len(scorers))
	}

	ctx := context.Background()
	for _, s := range scorers {
		if s.Name() == "" {
			t.Errorf("%T has empty Name", s)
		}
		// ScoreResult must not panic; ToInvariant may be nil.
		if _, err := s.ScoreResult(ctx, scenarios[0]); err != nil {
			t.Errorf("%s.ScoreResult: %v", s.Name(), err)
		}
		_ = s.ToInvariant()
		_ = s.ToRule()
	}

	invs := ScorersToInvariants(scorers)
	if len(invs) == 0 {
		t.Fatalf("expected at least one scorer to produce an invariant")
	}
}

func TestOTelFlushHooks(t *testing.T) {
	var called atomic.Int32
	RegisterOtelFlush(func(ctx context.Context) error {
		called.Add(1)
		return nil
	})
	RegisterOtelFlush(func(ctx context.Context) error {
		called.Add(1)
		return fmt.Errorf("boom")
	})
	err := FlushOtel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected first-error propagation, got %v", err)
	}
	if called.Load() != 2 {
		t.Fatalf("both callbacks should run, got %d", called.Load())
	}
}

func TestWrapTransportTracesAnthropic(t *testing.T) {
	// Stand up a fake Anthropic server and pipe it through the SDK's
	// transport wrapper.
	anthropicSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg-1",
			"model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "text", "text": "hi"},
				{"type": "tool_use", "id": "t1", "name": "get_weather", "input": map[string]string{"city": "NYC"}},
			},
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer anthropicSrv.Close()

	mock, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)

	httpClient := &http.Client{
		Transport: WrapTransport(ks, "sb-1", http.DefaultTransport),
		Timeout:   5 * time.Second,
	}
	req, _ := http.NewRequest("POST", anthropicSrv.URL+"/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Wait a moment for the fire-and-forget trace POST to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.traceBatches)
		mock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.traceBatches) == 0 {
		t.Fatalf("wrap transport didn't emit traces")
	}
	events := mock.traceBatches[0]
	if len(events) < 2 {
		t.Fatalf("expected llm_call + tool_use, got %d events", len(events))
	}
	var sawLLM, sawTool bool
	for _, e := range events {
		switch e.EventType {
		case "llm_call":
			sawLLM = true
			if e.Cost == nil || e.Cost.InputTokens != 10 || e.Cost.OutputTokens != 5 {
				t.Errorf("cost wrong: %+v", e.Cost)
			}
			if e.Metadata["gen_ai.system"] != "anthropic" {
				t.Errorf("OTel gen_ai metadata missing or wrong: %+v", e.Metadata)
			}
		case "tool_use":
			sawTool = true
			if e.ToolName != "get_weather" {
				t.Errorf("tool name wrong: %q", e.ToolName)
			}
		}
	}
	if !sawLLM || !sawTool {
		t.Fatalf("missing expected events — llm=%v tool=%v", sawLLM, sawTool)
	}
}

func TestTracingNestedSpans(t *testing.T) {
	m, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)

	tc := ks.InitTracing("sb-1")
	ctx := context.Background()

	err := tc.Traced(ctx, "outer", func() error {
		return tc.Traced(ctx, "inner", func() error {
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Traced: %v", err)
	}

	// Wait for fire-and-forget trace POSTs.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.traceBatches)
		m.mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Each Traced fires 2 events (start + end), so 4 total — each currently
	// posted in its own batch (one event per batch in Go).
	count := 0
	for _, batch := range m.traceBatches {
		count += len(batch)
	}
	if count < 4 {
		t.Fatalf("expected 4 span events, got %d", count)
	}
}

func TestAgentPageList(t *testing.T) {
	_, ts := newMockServer(t)
	defer ts.Close()
	ks := clientFor(ts)
	page, err := ks.Agents.List(context.Background(), WithLimit(10))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page == nil || len(page.Items) == 0 {
		t.Fatalf("no agents returned")
	}
}

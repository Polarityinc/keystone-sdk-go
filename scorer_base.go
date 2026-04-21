package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Scorer is the common interface every built-in scorer (and
// user-authored custom scorer) satisfies. Every method has a no-op default
// implementation on BaseScorer; concrete scorers override the methods they
// implement.
//
// Two execution models share the interface:
//
//   - Server-side invariant: ToInvariant() returns a non-nil Invariant the
//     server evaluates as part of the spec (file checks, SQL, LLM-as-judge).
//     ScoreResult returns (nil, nil) — there is nothing to compute client
//     side. Example: FileExists.
//
//   - Client-side score: ScoreResult returns a *Score computed in-process
//     (heuristic or LLM-judge via a local LLMClient). ToInvariant returns
//     nil. Example: Levenshtein.
//
// LLM-judge scorers support BOTH paths: calling ToInvariant() gives you a
// server-side rule; calling ScoreResult() runs locally using the configured
// LLMClient. ToRule() serialises either as a reusable /v1/score-rules entry.
type Scorer interface {
	// Name returns the stable identifier for the scorer.
	Name() string
	// Weight is the scorer's contribution to composite_score in [0, 1].
	Weight() float64
	// Gate returns true when a failing scorer must fail the entire scenario.
	Gate() bool
	// ToInvariant serialises the scorer as a spec invariant, or nil if
	// the scorer only runs client-side.
	ToInvariant() *Invariant
	// ToRule serialises the scorer as a reusable server-side rule, or nil
	// if the scorer has no persistent-rule equivalent.
	ToRule() *ScoreRulePayload
	// ScoreResult evaluates the scorer against a completed scenario,
	// returning the score (may be nil if the scorer doesn't apply).
	ScoreResult(ctx context.Context, scenario ScenarioResult) (*Score, error)
}

// Invariant is the server-side check shape emitted by built-in scorers.
type Invariant struct {
	Description string                 `json:"description"`
	Weight      float64                `json:"weight"`
	Gate        bool                   `json:"gate"`
	Check       map[string]interface{} `json:"check"`
}

// ScoreRulePayload is the POST body accepted by /v1/score-rules.
type ScoreRulePayload struct {
	Name   string                 `json:"name"`
	Type   string                 `json:"type"`
	Config map[string]interface{} `json:"config"`
}

// ─── BaseScorer — embed in every concrete scorer for defaults ───────────

// BaseScorer carries the common fields (name/weight/gate/rule_type) that
// every built-in scorer shares, plus no-op implementations of the Scorer
// interface methods. Concrete scorers embed it and override the relevant
// methods.
type BaseScorer struct {
	name     string
	weight   float64
	gate     bool
	RuleType string
}

// NewBaseScorer initialises a BaseScorer. Called by concrete scorer
// constructors.
func NewBaseScorer(name string, weight float64, gate bool) BaseScorer {
	if weight == 0 {
		weight = 1.0
	}
	return BaseScorer{name: name, weight: weight, gate: gate}
}

// Name satisfies Scorer.
func (b BaseScorer) Name() string { return b.name }

// Weight satisfies Scorer.
func (b BaseScorer) Weight() float64 { return b.weight }

// Gate satisfies Scorer.
func (b BaseScorer) Gate() bool { return b.gate }

// ToInvariant — default: client-side only.
func (b BaseScorer) ToInvariant() *Invariant { return nil }

// ToRule — default: no persistent-rule form.
func (b BaseScorer) ToRule() *ScoreRulePayload { return nil }

// ScoreResult — default: scorer doesn't apply to this scenario.
func (b BaseScorer) ScoreResult(_ context.Context, _ ScenarioResult) (*Score, error) {
	return nil, nil
}

// ─── LLMClient — hybrid-mode backend for LLM-judge scorers ──────────────

// LLMMessage mirrors the common `{role, content}` message shape.
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLMCompleteOpts carries the per-call model + temperature knobs.
type LLMCompleteOpts struct {
	Model       string
	Temperature float64
	MaxTokens   int
}

// LLMClient is the backend that LLM-judge scorers invoke when running
// client-side. Anything with a Complete method satisfies it; see
// ParagonProxyClient for the default implementation.
type LLMClient interface {
	Complete(ctx context.Context, messages []LLMMessage, opts LLMCompleteOpts) (string, error)
}

// ParagonProxyClient routes completions through paragon-llm-proxy — the
// SDK's default LLM backend per the Paragon rule in CLAUDE.md. Uses
// PARAGON_API_KEY (or KEYSTONE_API_KEY) from the environment when no key
// is passed to the constructor.
type ParagonProxyClient struct {
	ProxyURL string
	APIKey   string
	HTTP     *http.Client
}

// NewParagonProxyClient returns a client with sensible defaults.
func NewParagonProxyClient(apiKey string) *ParagonProxyClient {
	if apiKey == "" {
		apiKey = os.Getenv("PARAGON_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("KEYSTONE_API_KEY")
	}
	return &ParagonProxyClient{
		ProxyURL: "https://dmlkvjayquufspcpaqyq.supabase.co/functions/v1/paragon-llm-proxy/chat/completions",
		APIKey:   apiKey,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}
}

// Complete satisfies LLMClient.
func (p *ParagonProxyClient) Complete(ctx context.Context, messages []LLMMessage, opts LLMCompleteOpts) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
	}
	body, err := json.Marshal(map[string]interface{}{
		"model":       opts.Model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"max_tokens":  maxTokens,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ProxyURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	// Public supabase anon key. Safe to embed; it only authorises the
	// paragon-llm-proxy edge function invocation.
	const anonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6ImRtbGt2amF5cXV1ZnNwY3BhcXlxIiwicm9sZSI6ImFub24iLCJpYXQiOjE3MzYxOTMxNDUsImV4cCI6MjA1MTc2OTE0NX0.-zAXDD-cT-u1tzYn-I0qEoF0XQp3j4nNgl9xUaa-_NU"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+anonKey)
	req.Header.Set("apikey", anonKey)
	req.Header.Set("x-paragon-api-key", p.APIKey)
	req.Header.Set("x-paragon-client", "keystone-sdk-go")

	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("paragon-llm-proxy %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", fmt.Errorf("unparseable proxy response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// AdaptAnyClient wraps an arbitrary client whose shape we recognise into
// the LLMClient protocol. Recognises paragon-style, OpenAI-style, and
// objects that already satisfy LLMClient natively.
func AdaptAnyClient(client interface{}) LLMClient {
	if c, ok := client.(LLMClient); ok {
		return c
	}
	// OpenAI-style clients expose .Chat.Completions.Create — wire them up
	// dynamically once callers need this; for the default path most users
	// will just pass a ParagonProxyClient or their own implementation.
	panic("keystone: AdaptAnyClient expects an LLMClient; wrap your provider client yourself for now")
}

// ─── Module-level default client ────────────────────────────────────────

var defaultClient LLMClient

// DefaultLLMClient returns the module-global default. Lazily constructed.
func DefaultLLMClient() LLMClient {
	if defaultClient == nil {
		defaultClient = NewParagonProxyClient("")
	}
	return defaultClient
}

// SetDefaultLLMClient overrides the module-global default. Useful for
// testing or for pointing every LLM-judge scorer at a single client.
func SetDefaultLLMClient(c LLMClient) {
	defaultClient = c
}

// ─── CustomScorer — user-authored scoring function ──────────────────────

// ScorerFunc is the signature of a user-authored scoring function. Returning
// a float (0-1), a bool, a Score, or nil is supported via the helper
// NormaliseScore.
type ScorerFunc func(ctx context.Context, scenario ScenarioResult) (any, error)

// CustomScorer wraps a user function. Satisfies Scorer.
type CustomScorer struct {
	BaseScorer
	fn ScorerFunc
}

// ScoreResult runs the wrapped function and normalises its return value.
func (c *CustomScorer) ScoreResult(ctx context.Context, scenario ScenarioResult) (*Score, error) {
	out, err := c.fn(ctx, scenario)
	if err != nil {
		return &Score{Name: c.name, Score: 0, Passed: false, Message: fmt.Sprintf("scorer error: %v", err)}, nil
	}
	return NormaliseScore(c.name, out), nil
}

// CustomScorerOpt configures a custom scorer.
type CustomScorerOpt func(*CustomScorer)

// CustomName sets the scorer's name.
func CustomName(name string) CustomScorerOpt {
	return func(c *CustomScorer) { c.name = name }
}

// CustomWeight sets the weight.
func CustomWeight(w float64) CustomScorerOpt {
	return func(c *CustomScorer) { c.weight = w }
}

// CustomGate sets whether this scorer gates the scenario.
func CustomGate(gate bool) CustomScorerOpt {
	return func(c *CustomScorer) { c.gate = gate }
}

// NewScorer wraps fn as a CustomScorer. Go equivalent of Python's @Scorer
// decorator and TypeScript's `scorer()` helper.
//
//	mine := keystone.NewScorer(func(ctx context.Context, s keystone.ScenarioResult) (any, error) {
//	    return strings.Contains(s.AgentOutput, "ok"), nil
//	}, keystone.CustomName("contains-ok"))
func NewScorer(fn ScorerFunc, opts ...CustomScorerOpt) *CustomScorer {
	c := &CustomScorer{
		BaseScorer: NewBaseScorer("custom_scorer", 1.0, false),
		fn:         fn,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NormaliseScore converts a scorer's raw return value (float, bool, Score)
// into a canonical *Score.
func NormaliseScore(name string, out any) *Score {
	if out == nil {
		return nil
	}
	switch v := out.(type) {
	case Score:
		return &v
	case *Score:
		return v
	case float64:
		return &Score{Name: name, Score: v, Passed: v >= 0.5}
	case float32:
		return &Score{Name: name, Score: float64(v), Passed: float64(v) >= 0.5}
	case int:
		return &Score{Name: name, Score: float64(v), Passed: float64(v) >= 0.5}
	case bool:
		s := 0.0
		if v {
			s = 1.0
		}
		return &Score{Name: name, Score: s, Passed: v}
	}
	return nil
}

// ─── Bulk helpers ───────────────────────────────────────────────────────

// ScorersToInvariants converts a slice of scorers to a map of
// server-side invariants, keyed by a sanitised scorer name. Scorers that
// don't produce invariants are skipped.
func ScorersToInvariants(scorers []Scorer) map[string]Invariant {
	out := make(map[string]Invariant, len(scorers))
	for _, s := range scorers {
		inv := s.ToInvariant()
		if inv == nil {
			continue
		}
		key := sanitizeKey(s.Name())
		out[key] = *inv
	}
	return out
}

// RunClientScorers evaluates every scorer against the scenario and returns
// the resulting Scores in definition order. Scorer errors are captured on
// the Score.Message and do not abort the batch.
func RunClientScorers(ctx context.Context, scorers []Scorer, scenario ScenarioResult) []Score {
	results := make([]Score, 0, len(scorers))
	for _, s := range scorers {
		score, err := s.ScoreResult(ctx, scenario)
		if err != nil {
			results = append(results, Score{
				Name:    s.Name(),
				Score:   0,
				Passed:  false,
				Message: fmt.Sprintf("scorer error: %v", err),
			})
			continue
		}
		if score != nil {
			results = append(results, *score)
		}
	}
	return results
}

// ScoresToInvariantResults converts Score values into InvariantResult rows
// so they can be merged into a ScenarioResult.Invariants slice.
func ScoresToInvariantResults(scores []Score) []InvariantResult {
	out := make([]InvariantResult, len(scores))
	for i, s := range scores {
		out[i] = InvariantResult{
			Name:    s.Name,
			Passed:  s.Passed,
			Score:   s.Score,
			Weight:  1.0,
			Message: s.Message,
		}
	}
	return out
}

func sanitizeKey(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case ':', '/', ' ':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// compileGuard keeps the `time` import used even if individual files don't
// reference it. Harmless — removed by the Go compiler dead-code pass.
var _ = time.Second

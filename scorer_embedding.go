package keystone

import (
	"context"
	"fmt"
	"math"
)

// Embedder is any function that embeds a string into a numeric vector.
type Embedder func(ctx context.Context, text string) ([]float64, error)

// EmbeddingSimilarity scores the cosine similarity between the embedded
// agent_output and the embedded expected string.
type EmbeddingSimilarity struct {
	BaseScorer
	Expected    string
	ExpectedKey string
	Threshold   float64
	EmbedModel  string
	Embedder    Embedder
	hasExpected bool
}

// EmbeddingSimilarityOpt configures the scorer.
type EmbeddingSimilarityOpt func(*EmbeddingSimilarity)

// ESExpected supplies an inline expected string.
func ESExpected(v string) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.Expected = v; e.hasExpected = true }
}

// ESExpectedKey pulls the expected string from scenario.Parameters[k].
func ESExpectedKey(k string) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.ExpectedKey = k }
}

// ESThreshold sets the passing cosine similarity.
func ESThreshold(t float64) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.Threshold = t }
}

// ESModel tags the score message with a model name (informational only —
// the Embedder is responsible for model selection).
func ESModel(m string) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.EmbedModel = m }
}

// ESWeight sets the scorer weight.
func ESWeight(w float64) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.weight = w }
}

// ESGate marks the scorer as a gate.
func ESGate(g bool) EmbeddingSimilarityOpt {
	return func(e *EmbeddingSimilarity) { e.gate = g }
}

// NewEmbeddingSimilarity constructs the scorer. The embedder is required.
func NewEmbeddingSimilarity(embedder Embedder, opts ...EmbeddingSimilarityOpt) *EmbeddingSimilarity {
	if embedder == nil {
		panic("keystone: NewEmbeddingSimilarity requires a non-nil Embedder")
	}
	e := &EmbeddingSimilarity{
		BaseScorer: NewBaseScorer("embedding_similarity", 1.0, false),
		Threshold:  0.85,
		EmbedModel: "custom",
		Embedder:   embedder,
	}
	e.RuleType = "embedding_similarity"
	for _, o := range opts {
		o(e)
	}
	return e
}

// ScoreResult satisfies Scorer.
func (e *EmbeddingSimilarity) ScoreResult(ctx context.Context, scenario ScenarioResult) (*Score, error) {
	var expected string
	if e.hasExpected {
		expected = e.Expected
	} else if v, ok := paramString(scenario, e.ExpectedKey); ok {
		expected = v
	} else {
		return nil, nil
	}
	if scenario.AgentOutput == "" {
		return &Score{Name: e.Name(), Score: 0, Passed: false, Message: "empty output"}, nil
	}
	aEmb, err := e.Embedder(ctx, scenario.AgentOutput)
	if err != nil {
		return &Score{Name: e.Name(), Score: 0, Passed: false, Message: fmt.Sprintf("embedder failed: %v", err)}, nil
	}
	bEmb, err := e.Embedder(ctx, expected)
	if err != nil {
		return &Score{Name: e.Name(), Score: 0, Passed: false, Message: fmt.Sprintf("embedder failed: %v", err)}, nil
	}
	sim := cosineSimilarity(aEmb, bEmb)
	return &Score{
		Name:    e.Name(),
		Score:   sim,
		Passed:  sim >= e.Threshold,
		Message: fmt.Sprintf("cosine=%.4f threshold=%.2f model=%s", sim, e.Threshold, e.EmbedModel),
	}, nil
}

// ToRule satisfies Scorer.
func (e *EmbeddingSimilarity) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: e.Name(),
		Type: e.RuleType,
		Config: map[string]interface{}{
			"expected":     e.Expected,
			"expected_key": e.ExpectedKey,
			"threshold":    e.Threshold,
			"model":        e.EmbedModel,
		},
	}
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

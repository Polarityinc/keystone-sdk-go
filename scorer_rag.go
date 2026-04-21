package keystone

import "context"

// ragConfig carries the three keys every RAG scorer pulls from the
// ScenarioResult.Parameters map.
type ragConfig struct {
	QuestionKey string
	ContextKey  string
	ExpectedKey string
}

// RAGOpt configures a RAG scorer. These options *set* the parameter keys
// used for lookup; pass-through JudgeOpts continue to work via JudgeModel /
// JudgeTemperature / JudgeRubric / JudgeClient etc.
type RAGOpt func(*ragConfig)

// RAGQuestionKey overrides the parameter key for the user question.
func RAGQuestionKey(k string) RAGOpt { return func(c *ragConfig) { c.QuestionKey = k } }

// RAGContextKey overrides the parameter key for the retrieved context.
func RAGContextKey(k string) RAGOpt { return func(c *ragConfig) { c.ContextKey = k } }

// RAGExpectedKey overrides the parameter key for the reference answer.
func RAGExpectedKey(k string) RAGOpt { return func(c *ragConfig) { c.ExpectedKey = k } }

func newRAGConfig(opts ...RAGOpt) ragConfig {
	c := ragConfig{
		QuestionKey: "question",
		ContextKey:  "context",
		ExpectedKey: "expected",
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func formatContext(raw string) string {
	if raw == "" {
		return "(no context)"
	}
	return raw
}

// splitOpts partitions option slices. Callers can pass both RAGOpt and
// JudgeOpt values by constructing with the typed option slices directly;
// we provide two constructor arg lists for clarity.
type ragScorer struct {
	judgeScorerBase
	rag ragConfig
}

func newRAGScorer(name, templateKey string, rag ragConfig, opts []JudgeOpt) ragScorer {
	return ragScorer{
		judgeScorerBase: newJudgeScorerBase(name, templateKey, opts),
		rag:             rag,
	}
}

// ─── Context-side scorers ───────────────────────────────────────────────

// ContextPrecision — fraction of retrieved chunks relevant to the reference answer.
type ContextPrecision struct{ ragScorer }

// NewContextPrecision constructs the scorer. Pass RAGOpts for keys and
// JudgeOpts for model/temperature/client/etc.
func NewContextPrecision(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *ContextPrecision {
	cp := &ContextPrecision{}
	cp.ragScorer = newRAGScorer("context_precision", "context_precision", newRAGConfig(ragOpts...), judgeOpts)
	cp.buildPrompt = func(s ScenarioResult) (string, error) {
		exp, err := requireParam(s, cp.rag.ExpectedKey)
		if err != nil {
			return "", err
		}
		ctx, _ := paramString(s, cp.rag.ContextKey)
		return fillTemplate(cp.template(), map[string]string{
			"expected": exp, "context": formatContext(ctx),
		}), nil
	}
	return cp
}

func (cp *ContextPrecision) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return cp.scoreWithClient(ctx, s)
}
func (cp *ContextPrecision) ToInvariant() *Invariant   { return cp.toInvariant() }
func (cp *ContextPrecision) ToRule() *ScoreRulePayload { return cp.toRule() }

// ContextRecall
type ContextRecall struct{ ragScorer }

func NewContextRecall(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *ContextRecall {
	cr := &ContextRecall{}
	cr.ragScorer = newRAGScorer("context_recall", "context_recall", newRAGConfig(ragOpts...), judgeOpts)
	cr.buildPrompt = func(s ScenarioResult) (string, error) {
		exp, err := requireParam(s, cr.rag.ExpectedKey)
		if err != nil {
			return "", err
		}
		ctx, _ := paramString(s, cr.rag.ContextKey)
		return fillTemplate(cr.template(), map[string]string{
			"expected": exp, "context": formatContext(ctx),
		}), nil
	}
	return cr
}

func (cr *ContextRecall) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return cr.scoreWithClient(ctx, s)
}
func (cr *ContextRecall) ToInvariant() *Invariant   { return cr.toInvariant() }
func (cr *ContextRecall) ToRule() *ScoreRulePayload { return cr.toRule() }

// ContextRelevancy
type ContextRelevancy struct{ ragScorer }

func NewContextRelevancy(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *ContextRelevancy {
	cr := &ContextRelevancy{}
	cr.ragScorer = newRAGScorer("context_relevancy", "context_relevancy", newRAGConfig(ragOpts...), judgeOpts)
	cr.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, cr.rag.QuestionKey)
		if err != nil {
			return "", err
		}
		ctx, _ := paramString(s, cr.rag.ContextKey)
		return fillTemplate(cr.template(), map[string]string{
			"question": q, "context": formatContext(ctx),
		}), nil
	}
	return cr
}

func (cr *ContextRelevancy) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return cr.scoreWithClient(ctx, s)
}
func (cr *ContextRelevancy) ToInvariant() *Invariant   { return cr.toInvariant() }
func (cr *ContextRelevancy) ToRule() *ScoreRulePayload { return cr.toRule() }

// ContextEntityRecall
type ContextEntityRecall struct{ ragScorer }

func NewContextEntityRecall(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *ContextEntityRecall {
	cer := &ContextEntityRecall{}
	cer.ragScorer = newRAGScorer("context_entity_recall", "context_entity_recall", newRAGConfig(ragOpts...), judgeOpts)
	cer.buildPrompt = func(s ScenarioResult) (string, error) {
		exp, err := requireParam(s, cer.rag.ExpectedKey)
		if err != nil {
			return "", err
		}
		ctx, _ := paramString(s, cer.rag.ContextKey)
		return fillTemplate(cer.template(), map[string]string{
			"expected": exp, "context": formatContext(ctx),
		}), nil
	}
	return cer
}

func (cer *ContextEntityRecall) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return cer.scoreWithClient(ctx, s)
}
func (cer *ContextEntityRecall) ToInvariant() *Invariant   { return cer.toInvariant() }
func (cer *ContextEntityRecall) ToRule() *ScoreRulePayload { return cer.toRule() }

// ─── Answer-side scorers ────────────────────────────────────────────────

// Faithfulness — every claim in the answer must be supported by the context.
type Faithfulness struct{ ragScorer }

func NewFaithfulness(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *Faithfulness {
	f := &Faithfulness{}
	f.ragScorer = newRAGScorer("faithfulness", "faithfulness", newRAGConfig(ragOpts...), judgeOpts)
	f.buildPrompt = func(s ScenarioResult) (string, error) {
		ctx, _ := paramString(s, f.rag.ContextKey)
		return fillTemplate(f.template(), map[string]string{
			"context": formatContext(ctx), "actual": s.AgentOutput,
		}), nil
	}
	return f
}

func (f *Faithfulness) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return f.scoreWithClient(ctx, s)
}
func (f *Faithfulness) ToInvariant() *Invariant   { return f.toInvariant() }
func (f *Faithfulness) ToRule() *ScoreRulePayload { return f.toRule() }

// AnswerRelevancy
type AnswerRelevancy struct{ ragScorer }

func NewAnswerRelevancy(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *AnswerRelevancy {
	ar := &AnswerRelevancy{}
	ar.ragScorer = newRAGScorer("answer_relevancy", "answer_relevancy", newRAGConfig(ragOpts...), judgeOpts)
	ar.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, ar.rag.QuestionKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(ar.template(), map[string]string{
			"question": q, "actual": s.AgentOutput,
		}), nil
	}
	return ar
}

func (ar *AnswerRelevancy) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return ar.scoreWithClient(ctx, s)
}
func (ar *AnswerRelevancy) ToInvariant() *Invariant   { return ar.toInvariant() }
func (ar *AnswerRelevancy) ToRule() *ScoreRulePayload { return ar.toRule() }

// AnswerSimilarity
type AnswerSimilarity struct{ ragScorer }

func NewAnswerSimilarity(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *AnswerSimilarity {
	as := &AnswerSimilarity{}
	as.ragScorer = newRAGScorer("answer_similarity", "answer_similarity", newRAGConfig(ragOpts...), judgeOpts)
	as.buildPrompt = func(s ScenarioResult) (string, error) {
		exp, err := requireParam(s, as.rag.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(as.template(), map[string]string{
			"expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return as
}

func (as *AnswerSimilarity) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return as.scoreWithClient(ctx, s)
}
func (as *AnswerSimilarity) ToInvariant() *Invariant   { return as.toInvariant() }
func (as *AnswerSimilarity) ToRule() *ScoreRulePayload { return as.toRule() }

// AnswerCorrectness
type AnswerCorrectness struct{ ragScorer }

func NewAnswerCorrectness(ragOpts []RAGOpt, judgeOpts ...JudgeOpt) *AnswerCorrectness {
	ac := &AnswerCorrectness{}
	ac.ragScorer = newRAGScorer("answer_correctness", "answer_correctness", newRAGConfig(ragOpts...), judgeOpts)
	ac.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, ac.rag.QuestionKey)
		if err != nil {
			return "", err
		}
		exp, err := requireParam(s, ac.rag.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(ac.template(), map[string]string{
			"question": q, "expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return ac
}

func (ac *AnswerCorrectness) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return ac.scoreWithClient(ctx, s)
}
func (ac *AnswerCorrectness) ToInvariant() *Invariant   { return ac.toInvariant() }
func (ac *AnswerCorrectness) ToRule() *ScoreRulePayload { return ac.toRule() }

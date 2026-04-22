package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// ─── Default parser ─────────────────────────────────────────────────────

// JudgeResponseParser parses an LLM judge response into a Score.
type JudgeResponseParser func(name, response string) *Score

var judgeJSONRE = regexp.MustCompile(`\{[^{}]*\}`)

// DefaultJudgeParser parses the canonical {"score","passed","reason"}
// JSON envelope. Falls back to heuristic number extraction when the model
// doesn't comply with the JSON instruction.
func DefaultJudgeParser(name, response string) *Score {
	stripped := strings.TrimSpace(response)
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(stripped), &obj); err != nil {
		if m := judgeJSONRE.FindString(stripped); m != "" {
			_ = json.Unmarshal([]byte(m), &obj)
		}
	}
	if obj != nil {
		if rawScore, ok := obj["score"]; ok {
			score := toFloat64(rawScore)
			score = math.Max(0, math.Min(1, score))
			passed, hasPassed := obj["passed"].(bool)
			if !hasPassed {
				passed = score >= 0.5
			}
			reason, _ := obj["reason"].(string)
			return &Score{Name: name, Score: score, Passed: passed, Message: reason}
		}
	}

	// Heuristic fallback — first decimal in the response.
	m := regexp.MustCompile(`-?\d+(?:\.\d+)?`).FindString(stripped)
	if m != "" {
		var raw float64
		if _, err := fmt.Sscanf(m, "%f", &raw); err == nil {
			s := raw
			if s > 1 {
				s = s / 10
			}
			s = math.Max(0, math.Min(1, s))
			return &Score{
				Name:    name,
				Score:   s,
				Passed:  s >= 0.5,
				Message: fmt.Sprintf("heuristic parse: %s", truncate(stripped, 120)),
			}
		}
	}
	return &Score{
		Name:    name,
		Score:   0,
		Passed:  false,
		Message: fmt.Sprintf("unparseable: %s", truncate(stripped, 120)),
	}
}

func toFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
			return f
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// maxJudgeField caps user-controlled values before they flow into a judge
// prompt. Prevents pathological inputs from bloating prompts (cost DoS) and
// bounds the surface area a prompt-injection payload can use.
const maxJudgeField = 8000

func capField(s string) string {
	if len(s) <= maxJudgeField {
		return s
	}
	return s[:maxJudgeField] + "\n…[truncated]"
}

func fillTemplate(template string, vars map[string]string) string {
	for k, v := range vars {
		template = strings.ReplaceAll(template, "{"+k+"}", capField(v))
	}
	return template
}

// ─── JudgeScorer — shared plumbing for every LLM-judge ──────────────────

// JudgeConfig holds the tunable knobs every LLM-judge scorer accepts.
// Individual scorers construct a JudgeScorer via embedding and override
// whichever field they need via their option functions.
type JudgeConfig struct {
	Model          string
	Temperature    float64
	Rubric         map[string]string
	PromptTemplate string
	Parser         JudgeResponseParser
	Client         LLMClient
}

// JudgeOpt configures any LLM-judge scorer.
type JudgeOpt func(*JudgeConfig)

// JudgeModel overrides the model name.
func JudgeModel(m string) JudgeOpt { return func(c *JudgeConfig) { c.Model = m } }

// JudgeTemperature sets the sampling temperature.
func JudgeTemperature(t float64) JudgeOpt { return func(c *JudgeConfig) { c.Temperature = t } }

// JudgeRubric attaches a grading rubric.
func JudgeRubric(r map[string]string) JudgeOpt { return func(c *JudgeConfig) { c.Rubric = r } }

// JudgePromptTemplate overrides the prompt template used by the scorer.
func JudgePromptTemplate(t string) JudgeOpt { return func(c *JudgeConfig) { c.PromptTemplate = t } }

// JudgeParser overrides the response parser.
func JudgeParser(p JudgeResponseParser) JudgeOpt { return func(c *JudgeConfig) { c.Parser = p } }

// JudgeClient injects a specific LLMClient for this scorer.
func JudgeClient(c LLMClient) JudgeOpt { return func(cfg *JudgeConfig) { cfg.Client = c } }

// judgeScorerBase holds the BaseScorer + JudgeConfig shared state and the
// scorer-specific template-key / prompt-building callback.
type judgeScorerBase struct {
	BaseScorer
	cfg          JudgeConfig
	templateKey  string
	buildPrompt  func(scenario ScenarioResult) (string, error)
	invariantIn  string // input_from field in the server invariant
}

func newJudgeScorerBase(name, templateKey string, opts []JudgeOpt) judgeScorerBase {
	cfg := JudgeConfig{
		Model:       "paragon-fast",
		Temperature: 0,
	}
	for _, o := range opts {
		o(&cfg)
	}
	b := judgeScorerBase{
		BaseScorer:  NewBaseScorer(name, 1.0, false),
		cfg:         cfg,
		templateKey: templateKey,
		invariantIn: "agent_output",
	}
	b.RuleType = "llm_as_judge"
	return b
}

func (j *judgeScorerBase) template() string {
	if j.cfg.PromptTemplate != "" {
		return j.cfg.PromptTemplate
	}
	return JudgePromptTemplates[j.templateKey]
}

// scoreWithClient runs the shared LLM call + parser flow.
func (j *judgeScorerBase) scoreWithClient(ctx context.Context, scenario ScenarioResult) (*Score, error) {
	userPrompt, err := j.buildPrompt(scenario)
	if err != nil {
		return &Score{Name: j.Name(), Score: 0, Passed: false, Message: err.Error()}, nil
	}
	if len(j.cfg.Rubric) > 0 {
		// Cap rubric size (prompt-injection hardening) and label it as
		// author-controlled so the judge treats embedded text as rules
		// metadata, not injected directives.
		const maxRubricChars = 2000
		var b strings.Builder
		b.WriteString("\nRubric (authored by the rule owner):\n")
		total := 0
		for k, v := range j.cfg.Rubric {
			line := "- " + k + ": " + v + "\n"
			total += len(line)
			if total > maxRubricChars {
				b.WriteString("- …rubric truncated (size cap reached)…\n")
				break
			}
			b.WriteString(line)
		}
		userPrompt += b.String()
	}
	client := j.cfg.Client
	if client == nil {
		client = DefaultLLMClient()
	}
	messages := []LLMMessage{
		{Role: "system", Content: JudgeSystem},
		{Role: "user", Content: userPrompt},
	}
	resp, err := client.Complete(ctx, messages, LLMCompleteOpts{
		Model:       j.cfg.Model,
		Temperature: j.cfg.Temperature,
		MaxTokens:   512,
	})
	if err != nil {
		return &Score{
			Name:    j.Name(),
			Score:   0,
			Passed:  false,
			Message: fmt.Sprintf("judge call failed: %v", err),
		}, nil
	}
	parser := j.cfg.Parser
	if parser == nil {
		parser = DefaultJudgeParser
	}
	return parser(j.Name(), resp), nil
}

func (j *judgeScorerBase) toInvariant() *Invariant {
	check := map[string]interface{}{
		"type":         "llm_as_judge",
		"model":        j.cfg.Model,
		"temperature":  j.cfg.Temperature,
		"template_key": j.templateKey,
		"input_from":   j.invariantIn,
	}
	if len(j.cfg.Rubric) > 0 {
		check["rubric"] = j.cfg.Rubric
	}
	if j.cfg.PromptTemplate != "" {
		check["prompt_template"] = j.cfg.PromptTemplate
	}
	return &Invariant{
		Description: j.Name(),
		Weight:      j.Weight(),
		Gate:        j.Gate(),
		Check:       check,
	}
}

func (j *judgeScorerBase) toRule() *ScoreRulePayload {
	cfg := map[string]interface{}{
		"template_key": j.templateKey,
		"model":        j.cfg.Model,
		"temperature":  j.cfg.Temperature,
	}
	if len(j.cfg.Rubric) > 0 {
		cfg["rubric"] = j.cfg.Rubric
	}
	if j.cfg.PromptTemplate != "" {
		cfg["prompt_template"] = j.cfg.PromptTemplate
	}
	return &ScoreRulePayload{Name: j.Name(), Type: "llm_as_judge", Config: cfg}
}

// param pulls a required parameter out of ScenarioResult.Parameters, returning
// an error message suitable for the Score.Message field.
func requireParam(scenario ScenarioResult, key string) (string, error) {
	if v, ok := paramString(scenario, key); ok {
		return v, nil
	}
	return "", fmt.Errorf("scenario.Parameters missing %q", key)
}

// ─── Concrete judge scorers ─────────────────────────────────────────────

// Factuality grades factual accuracy vs a reference answer.
type Factuality struct {
	judgeScorerBase
	QuestionKey string
	ExpectedKey string
}

// NewFactuality constructs a Factuality scorer. Scenario parameters must
// supply the question and expected answer under their respective keys.
func NewFactuality(opts ...JudgeOpt) *Factuality {
	f := &Factuality{
		QuestionKey: "question",
		ExpectedKey: "expected",
	}
	f.judgeScorerBase = newJudgeScorerBase("factuality", "factuality", opts)
	f.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, f.QuestionKey)
		if err != nil {
			return "", err
		}
		exp, err := requireParam(s, f.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(f.template(), map[string]string{
			"question": q, "expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return f
}

func (f *Factuality) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return f.scoreWithClient(ctx, s)
}
func (f *Factuality) ToInvariant() *Invariant       { return f.toInvariant() }
func (f *Factuality) ToRule() *ScoreRulePayload     { return f.toRule() }

// Battle A/B-grades the agent answer vs a baseline.
type Battle struct {
	judgeScorerBase
	InstructionKey string
	ExpectedKey    string
}

func NewBattle(opts ...JudgeOpt) *Battle {
	b := &Battle{InstructionKey: "instruction", ExpectedKey: "baseline"}
	b.judgeScorerBase = newJudgeScorerBase("battle", "battle", opts)
	b.buildPrompt = func(s ScenarioResult) (string, error) {
		ins, err := requireParam(s, b.InstructionKey)
		if err != nil {
			return "", err
		}
		exp, err := requireParam(s, b.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(b.template(), map[string]string{
			"instruction": ins, "expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return b
}

func (b *Battle) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return b.scoreWithClient(ctx, s)
}
func (b *Battle) ToInvariant() *Invariant   { return b.toInvariant() }
func (b *Battle) ToRule() *ScoreRulePayload { return b.toRule() }

// ClosedQA grades a closed-form QA response.
type ClosedQA struct {
	judgeScorerBase
	QuestionKey string
	ExpectedKey string
}

func NewClosedQA(opts ...JudgeOpt) *ClosedQA {
	c := &ClosedQA{QuestionKey: "question", ExpectedKey: "answer"}
	c.judgeScorerBase = newJudgeScorerBase("closed_qa", "closed_qa", opts)
	c.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, c.QuestionKey)
		if err != nil {
			return "", err
		}
		exp, err := requireParam(s, c.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(c.template(), map[string]string{
			"question": q, "expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return c
}

func (c *ClosedQA) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return c.scoreWithClient(ctx, s)
}
func (c *ClosedQA) ToInvariant() *Invariant   { return c.toInvariant() }
func (c *ClosedQA) ToRule() *ScoreRulePayload { return c.toRule() }

// Humor rates the humour of agent_output.
type Humor struct{ judgeScorerBase }

func NewHumor(opts ...JudgeOpt) *Humor {
	h := &Humor{}
	h.judgeScorerBase = newJudgeScorerBase("humor", "humor", opts)
	h.buildPrompt = func(s ScenarioResult) (string, error) {
		return fillTemplate(h.template(), map[string]string{"actual": s.AgentOutput}), nil
	}
	return h
}

func (h *Humor) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return h.scoreWithClient(ctx, s)
}
func (h *Humor) ToInvariant() *Invariant   { return h.toInvariant() }
func (h *Humor) ToRule() *ScoreRulePayload { return h.toRule() }

// Moderation flags unsafe content.
type Moderation struct{ judgeScorerBase }

func NewModeration(opts ...JudgeOpt) *Moderation {
	m := &Moderation{}
	m.judgeScorerBase = newJudgeScorerBase("moderation", "moderation", opts)
	m.buildPrompt = func(s ScenarioResult) (string, error) {
		return fillTemplate(m.template(), map[string]string{"actual": s.AgentOutput}), nil
	}
	return m
}

func (m *Moderation) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return m.scoreWithClient(ctx, s)
}
func (m *Moderation) ToInvariant() *Invariant   { return m.toInvariant() }
func (m *Moderation) ToRule() *ScoreRulePayload { return m.toRule() }

// Summarization grades how well agent_output summarises a source.
type Summarization struct {
	judgeScorerBase
	SourceKey string
}

func NewSummarization(opts ...JudgeOpt) *Summarization {
	sm := &Summarization{SourceKey: "source"}
	sm.judgeScorerBase = newJudgeScorerBase("summarization", "summarization", opts)
	sm.buildPrompt = func(s ScenarioResult) (string, error) {
		src, err := requireParam(s, sm.SourceKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(sm.template(), map[string]string{
			"source": src, "actual": s.AgentOutput,
		}), nil
	}
	return sm
}

func (sm *Summarization) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return sm.scoreWithClient(ctx, s)
}
func (sm *Summarization) ToInvariant() *Invariant   { return sm.toInvariant() }
func (sm *Summarization) ToRule() *ScoreRulePayload { return sm.toRule() }

// SQLJudge compares generated SQL against an expected query.
type SQLJudge struct {
	judgeScorerBase
	QuestionKey string
	ExpectedKey string
}

func NewSQLJudge(opts ...JudgeOpt) *SQLJudge {
	sq := &SQLJudge{QuestionKey: "question", ExpectedKey: "expected_sql"}
	sq.judgeScorerBase = newJudgeScorerBase("sql_judge", "sql_judge", opts)
	sq.buildPrompt = func(s ScenarioResult) (string, error) {
		q, err := requireParam(s, sq.QuestionKey)
		if err != nil {
			return "", err
		}
		exp, err := requireParam(s, sq.ExpectedKey)
		if err != nil {
			return "", err
		}
		return fillTemplate(sq.template(), map[string]string{
			"question": q, "expected": exp, "actual": s.AgentOutput,
		}), nil
	}
	return sq
}

func (sq *SQLJudge) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return sq.scoreWithClient(ctx, s)
}
func (sq *SQLJudge) ToInvariant() *Invariant   { return sq.toInvariant() }
func (sq *SQLJudge) ToRule() *ScoreRulePayload { return sq.toRule() }

// Translation grades a translation from source_lang to target_lang.
type Translation struct {
	judgeScorerBase
	SourceKey   string
	SourceLang  string
	TargetLang  string
	ExpectedKey string
}

func NewTranslation(opts ...JudgeOpt) *Translation {
	t := &Translation{
		SourceKey:   "source",
		SourceLang:  "source language",
		TargetLang:  "target language",
		ExpectedKey: "reference",
	}
	t.judgeScorerBase = newJudgeScorerBase("translation", "translation", opts)
	t.buildPrompt = func(s ScenarioResult) (string, error) {
		src, err := requireParam(s, t.SourceKey)
		if err != nil {
			return "", err
		}
		exp, _ := paramString(s, t.ExpectedKey)
		if exp == "" {
			exp = "(no reference provided)"
		}
		return fillTemplate(t.template(), map[string]string{
			"source_lang": t.SourceLang,
			"target_lang": t.TargetLang,
			"source":      src,
			"actual":      s.AgentOutput,
			"expected":    exp,
		}), nil
	}
	return t
}

func (t *Translation) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return t.scoreWithClient(ctx, s)
}
func (t *Translation) ToInvariant() *Invariant   { return t.toInvariant() }
func (t *Translation) ToRule() *ScoreRulePayload { return t.toRule() }

// Security flags unsafe code / leaked secrets / prompt-injection success.
type Security struct{ judgeScorerBase }

func NewSecurity(opts ...JudgeOpt) *Security {
	se := &Security{}
	se.judgeScorerBase = newJudgeScorerBase("security", "security", opts)
	se.buildPrompt = func(s ScenarioResult) (string, error) {
		return fillTemplate(se.template(), map[string]string{"actual": s.AgentOutput}), nil
	}
	return se
}

func (se *Security) ScoreResult(ctx context.Context, s ScenarioResult) (*Score, error) {
	return se.scoreWithClient(ctx, s)
}
func (se *Security) ToInvariant() *Invariant   { return se.toInvariant() }
func (se *Security) ToRule() *ScoreRulePayload { return se.toRule() }

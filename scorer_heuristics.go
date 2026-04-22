package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
)

// ─── Shared helpers ─────────────────────────────────────────────────────

func paramString(scenario ScenarioResult, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	v, ok := scenario.Parameters[key]
	if !ok || v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	default:
		return fmt.Sprintf("%v", t), true
	}
}

func paramFloat(scenario ScenarioResult, key string) (float64, bool) {
	if key == "" {
		return 0, false
	}
	v, ok := scenario.Parameters[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(t, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func paramAny(scenario ScenarioResult, key string) (interface{}, bool) {
	if key == "" {
		return nil, false
	}
	v, ok := scenario.Parameters[key]
	return v, ok
}

// similarity computes a normalised Levenshtein ratio in [0, 1].
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	ra := []rune(a)
	rb := []rune(b)
	m, n := len(ra), len(rb)
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minI(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		copy(prev, curr)
	}
	distance := prev[n]
	maxLen := m
	if n > maxLen {
		maxLen = n
	}
	return 1 - float64(distance)/float64(maxLen)
}

func minI(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// ─── ExactMatch ─────────────────────────────────────────────────────────

// ExactMatchOpt configures an ExactMatch scorer.
type ExactMatchOpt func(*ExactMatch)

func EMExpected(v string) ExactMatchOpt       { return func(e *ExactMatch) { e.Expected = v; e.hasExpected = true } }
func EMExpectedKey(k string) ExactMatchOpt    { return func(e *ExactMatch) { e.ExpectedKey = k } }
func EMCaseSensitive(v bool) ExactMatchOpt    { return func(e *ExactMatch) { e.CaseSensitive = v } }
func EMStrip(v bool) ExactMatchOpt            { return func(e *ExactMatch) { e.Strip = v } }
func EMWeight(w float64) ExactMatchOpt        { return func(e *ExactMatch) { e.weight = w } }
func EMGate(g bool) ExactMatchOpt             { return func(e *ExactMatch) { e.gate = g } }

// ExactMatch scores 1.0 iff the agent_output equals the expected string
// after optional normalisation (strip + case fold).
type ExactMatch struct {
	BaseScorer
	Expected      string
	ExpectedKey   string
	CaseSensitive bool
	Strip         bool
	hasExpected   bool
}

// NewExactMatch constructs an ExactMatch scorer.
func NewExactMatch(opts ...ExactMatchOpt) *ExactMatch {
	e := &ExactMatch{
		BaseScorer:    NewBaseScorer("exact_match", 1.0, false),
		CaseSensitive: true,
		Strip:         true,
	}
	e.RuleType = "exact_match"
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *ExactMatch) normalize(s string) string {
	if e.Strip {
		s = strings.TrimSpace(s)
	}
	if !e.CaseSensitive {
		s = strings.ToLower(s)
	}
	return s
}

// ScoreResult satisfies Scorer.
func (e *ExactMatch) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var expected string
	if e.hasExpected {
		expected = e.Expected
	} else if v, ok := paramString(scenario, e.ExpectedKey); ok {
		expected = v
	} else {
		return nil, nil
	}
	actual := e.normalize(scenario.AgentOutput)
	expNorm := e.normalize(expected)
	if actual == expNorm {
		return &Score{Name: e.Name(), Score: 1, Passed: true}, nil
	}
	return &Score{
		Name:    e.Name(),
		Score:   0,
		Passed:  false,
		Message: fmt.Sprintf("expected %q, got %q", expNorm, actual),
	}, nil
}

// ToRule satisfies Scorer.
func (e *ExactMatch) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: e.Name(),
		Type: e.RuleType,
		Config: map[string]interface{}{
			"expected":       e.Expected,
			"expected_key":   e.ExpectedKey,
			"case_sensitive": e.CaseSensitive,
			"strip":          e.Strip,
		},
	}
}

// ─── Levenshtein ────────────────────────────────────────────────────────

type LevenshteinOpt func(*Levenshtein)

func LVExpected(v string) LevenshteinOpt     { return func(l *Levenshtein) { l.Expected = v; l.hasExpected = true } }
func LVExpectedKey(k string) LevenshteinOpt  { return func(l *Levenshtein) { l.ExpectedKey = k } }
func LVThreshold(t float64) LevenshteinOpt   { return func(l *Levenshtein) { l.Threshold = t } }
func LVWeight(w float64) LevenshteinOpt      { return func(l *Levenshtein) { l.weight = w } }
func LVGate(g bool) LevenshteinOpt           { return func(l *Levenshtein) { l.gate = g } }

// Levenshtein scores the edit-distance similarity between actual and
// expected, normalised to [0, 1].
type Levenshtein struct {
	BaseScorer
	Expected    string
	ExpectedKey string
	Threshold   float64
	hasExpected bool
}

// NewLevenshtein constructs a Levenshtein scorer.
func NewLevenshtein(opts ...LevenshteinOpt) *Levenshtein {
	l := &Levenshtein{
		BaseScorer: NewBaseScorer("levenshtein", 1.0, false),
		Threshold:  0.8,
	}
	l.RuleType = "levenshtein"
	for _, o := range opts {
		o(l)
	}
	return l
}

func (l *Levenshtein) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var expected string
	if l.hasExpected {
		expected = l.Expected
	} else if v, ok := paramString(scenario, l.ExpectedKey); ok {
		expected = v
	} else {
		return nil, nil
	}
	ratio := similarity(scenario.AgentOutput, expected)
	return &Score{
		Name:    l.Name(),
		Score:   ratio,
		Passed:  ratio >= l.Threshold,
		Message: fmt.Sprintf("similarity=%.3f threshold=%.2f", ratio, l.Threshold),
	}, nil
}

func (l *Levenshtein) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: l.Name(),
		Type: l.RuleType,
		Config: map[string]interface{}{
			"expected":     l.Expected,
			"expected_key": l.ExpectedKey,
			"threshold":    l.Threshold,
		},
	}
}

// ─── NumericDiff ────────────────────────────────────────────────────────

type NumericDiffOpt func(*NumericDiff)

func NDExpected(v float64) NumericDiffOpt      { return func(n *NumericDiff) { n.Expected = v; n.hasExpected = true } }
func NDExpectedKey(k string) NumericDiffOpt    { return func(n *NumericDiff) { n.ExpectedKey = k } }
func NDTolerance(t float64) NumericDiffOpt     { return func(n *NumericDiff) { n.Tolerance = t } }
func NDWeight(w float64) NumericDiffOpt        { return func(n *NumericDiff) { n.weight = w } }
func NDGate(g bool) NumericDiffOpt             { return func(n *NumericDiff) { n.gate = g } }

// NumericDiff extracts the first numeric value from agent_output and scores
// 1 - relative error, clamped at 0.
type NumericDiff struct {
	BaseScorer
	Expected    float64
	ExpectedKey string
	Tolerance   float64
	hasExpected bool
}

var numRE = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

func NewNumericDiff(opts ...NumericDiffOpt) *NumericDiff {
	n := &NumericDiff{
		BaseScorer: NewBaseScorer("numeric_diff", 1.0, false),
		Tolerance:  0.01,
	}
	n.RuleType = "numeric_diff"
	for _, o := range opts {
		o(n)
	}
	return n
}

func (n *NumericDiff) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var expected float64
	if n.hasExpected {
		expected = n.Expected
	} else if v, ok := paramFloat(scenario, n.ExpectedKey); ok {
		expected = v
	} else {
		return nil, nil
	}
	match := numRE.FindString(scenario.AgentOutput)
	if match == "" {
		return &Score{Name: n.Name(), Score: 0, Passed: false, Message: "no numeric value in output"}, nil
	}
	var actual float64
	if _, err := fmt.Sscanf(match, "%f", &actual); err != nil {
		return &Score{Name: n.Name(), Score: 0, Passed: false, Message: fmt.Sprintf("parse failed: %v", err)}, nil
	}
	abs := math.Abs(expected)
	if abs < 1e-9 {
		abs = 1e-9
	}
	rel := math.Abs(actual-expected) / abs
	score := 1.0 - rel
	if score < 0 {
		score = 0
	}
	return &Score{
		Name:    n.Name(),
		Score:   score,
		Passed:  rel <= n.Tolerance,
		Message: fmt.Sprintf("actual=%g expected=%g rel_err=%.4f", actual, expected, rel),
	}, nil
}

func (n *NumericDiff) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: n.Name(),
		Type: n.RuleType,
		Config: map[string]interface{}{
			"expected":     n.Expected,
			"expected_key": n.ExpectedKey,
			"tolerance":    n.Tolerance,
		},
	}
}

// ─── JSONDiff ───────────────────────────────────────────────────────────

type JSONDiffOpt func(*JSONDiff)

func JDExpected(v interface{}) JSONDiffOpt   { return func(j *JSONDiff) { j.Expected = v; j.hasExpected = true } }
func JDExpectedKey(k string) JSONDiffOpt     { return func(j *JSONDiff) { j.ExpectedKey = k } }
func JDThreshold(t float64) JSONDiffOpt      { return func(j *JSONDiff) { j.Threshold = t } }
func JDWeight(w float64) JSONDiffOpt         { return func(j *JSONDiff) { j.weight = w } }
func JDGate(g bool) JSONDiffOpt              { return func(j *JSONDiff) { j.gate = g } }

// JSONDiff computes the structural similarity between actual and expected
// JSON values, ignoring key order.
type JSONDiff struct {
	BaseScorer
	Expected    interface{}
	ExpectedKey string
	Threshold   float64
	hasExpected bool
}

func NewJSONDiff(opts ...JSONDiffOpt) *JSONDiff {
	j := &JSONDiff{
		BaseScorer: NewBaseScorer("json_diff", 1.0, false),
		Threshold:  1.0,
	}
	j.RuleType = "json_diff"
	for _, o := range opts {
		o(j)
	}
	return j
}

func jsonStructuralSimilarity(a, b interface{}) float64 {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return 0
	}
	switch av := a.(type) {
	case map[string]interface{}:
		bv := b.(map[string]interface{})
		keys := make(map[string]struct{}, len(av)+len(bv))
		for k := range av {
			keys[k] = struct{}{}
		}
		for k := range bv {
			keys[k] = struct{}{}
		}
		if len(keys) == 0 {
			return 1
		}
		sum := 0.0
		for k := range keys {
			sum += jsonStructuralSimilarity(av[k], bv[k])
		}
		return sum / float64(len(keys))
	case []interface{}:
		bv := b.([]interface{})
		maxLen := len(av)
		if len(bv) > maxLen {
			maxLen = len(bv)
		}
		if maxLen == 0 {
			return 1
		}
		sum := 0.0
		common := len(av)
		if len(bv) < common {
			common = len(bv)
		}
		for i := 0; i < common; i++ {
			sum += jsonStructuralSimilarity(av[i], bv[i])
		}
		return sum / float64(maxLen)
	default:
		if reflect.DeepEqual(a, b) {
			return 1
		}
		return 0
	}
}

func (j *JSONDiff) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var expected interface{}
	if j.hasExpected {
		expected = j.Expected
	} else if v, ok := paramAny(scenario, j.ExpectedKey); ok {
		expected = v
	} else {
		return nil, nil
	}
	if expected == nil {
		return nil, nil
	}
	var actual interface{}
	if err := json.Unmarshal([]byte(scenario.AgentOutput), &actual); err != nil {
		return &Score{Name: j.Name(), Score: 0, Passed: false, Message: "output is not valid JSON"}, nil
	}
	sim := jsonStructuralSimilarity(actual, expected)
	return &Score{
		Name:    j.Name(),
		Score:   sim,
		Passed:  sim >= j.Threshold,
		Message: fmt.Sprintf("structural_similarity=%.3f", sim),
	}, nil
}

func (j *JSONDiff) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: j.Name(),
		Type: j.RuleType,
		Config: map[string]interface{}{
			"expected":     j.Expected,
			"expected_key": j.ExpectedKey,
			"threshold":    j.Threshold,
		},
	}
}

// ─── JSONValidity ───────────────────────────────────────────────────────

// JSONValidity scores 1 iff agent_output parses as JSON.
type JSONValidity struct {
	BaseScorer
}

type JSONValidityOpt func(*JSONValidity)

func JVWeight(w float64) JSONValidityOpt { return func(j *JSONValidity) { j.weight = w } }
func JVGate(g bool) JSONValidityOpt      { return func(j *JSONValidity) { j.gate = g } }

func NewJSONValidity(opts ...JSONValidityOpt) *JSONValidity {
	j := &JSONValidity{BaseScorer: NewBaseScorer("json_validity", 1.0, false)}
	j.RuleType = "json_validity"
	for _, o := range opts {
		o(j)
	}
	return j
}

func (j *JSONValidity) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var sink interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(scenario.AgentOutput)), &sink); err != nil {
		return &Score{Name: j.Name(), Score: 0, Passed: false, Message: fmt.Sprintf("invalid JSON: %v", err)}, nil
	}
	return &Score{Name: j.Name(), Score: 1, Passed: true}, nil
}

// ─── SemanticListContains ───────────────────────────────────────────────

type SLCOpt func(*SemanticListContains)

func SLCExpected(items []string) SLCOpt     { return func(s *SemanticListContains) { s.Expected = items; s.hasExpected = true } }
func SLCExpectedKey(k string) SLCOpt        { return func(s *SemanticListContains) { s.ExpectedKey = k } }
func SLCFuzzy(v bool) SLCOpt                { return func(s *SemanticListContains) { s.Fuzzy = v } }
func SLCFuzzyThreshold(t float64) SLCOpt    { return func(s *SemanticListContains) { s.FuzzyThreshold = t } }
func SLCThreshold(t float64) SLCOpt         { return func(s *SemanticListContains) { s.Threshold = t } }
func SLCWeight(w float64) SLCOpt            { return func(s *SemanticListContains) { s.weight = w } }
func SLCGate(g bool) SLCOpt                 { return func(s *SemanticListContains) { s.gate = g } }

// SemanticListContains checks that every required item appears in
// agent_output, with optional fuzzy matching.
type SemanticListContains struct {
	BaseScorer
	Expected       []string
	ExpectedKey    string
	Fuzzy          bool
	FuzzyThreshold float64
	Threshold      float64
	hasExpected    bool
}

func NewSemanticListContains(opts ...SLCOpt) *SemanticListContains {
	s := &SemanticListContains{
		BaseScorer:     NewBaseScorer("semantic_list_contains", 1.0, false),
		FuzzyThreshold: 0.75,
		Threshold:      1.0,
	}
	s.RuleType = "semantic_list_contains"
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *SemanticListContains) match(needle, hay string) bool {
	n := strings.ToLower(needle)
	h := strings.ToLower(hay)
	if strings.Contains(h, n) {
		return true
	}
	if !s.Fuzzy {
		return false
	}
	return similarity(n, h) >= s.FuzzyThreshold
}

func (s *SemanticListContains) ScoreResult(_ context.Context, scenario ScenarioResult) (*Score, error) {
	var items []string
	if s.hasExpected {
		items = s.Expected
	} else if raw, ok := paramAny(scenario, s.ExpectedKey); ok {
		if list, ok := raw.([]interface{}); ok {
			for _, it := range list {
				items = append(items, fmt.Sprintf("%v", it))
			}
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	if scenario.AgentOutput == "" {
		return &Score{Name: s.Name(), Score: 0, Passed: false, Message: "empty output"}, nil
	}
	matched := 0
	for _, it := range items {
		if s.match(it, scenario.AgentOutput) {
			matched++
		}
	}
	score := float64(matched) / float64(len(items))
	return &Score{
		Name:    s.Name(),
		Score:   score,
		Passed:  score >= s.Threshold,
		Message: fmt.Sprintf("matched %d/%d", matched, len(items)),
	}, nil
}

func (s *SemanticListContains) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: s.Name(),
		Type: s.RuleType,
		Config: map[string]interface{}{
			"expected":        s.Expected,
			"expected_key":    s.ExpectedKey,
			"fuzzy":           s.Fuzzy,
			"fuzzy_threshold": s.FuzzyThreshold,
			"threshold":       s.Threshold,
		},
	}
}

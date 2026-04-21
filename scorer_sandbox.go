package keystone

// Sandbox-oriented scorers. These map to server-side invariants — the
// sandbox runner evaluates them after agent execution. ScoreResult is a
// no-op; ToInvariant returns the check spec.

// ─── FileExists ─────────────────────────────────────────────────────────

// FileExists checks that a file exists in the workspace.
type FileExists struct {
	BaseScorer
	Path string
}

// FileExistsOpt configures a FileExists scorer.
type FileExistsOpt func(*FileExists)

func FEWeight(w float64) FileExistsOpt { return func(f *FileExists) { f.weight = w } }
func FEGate(g bool) FileExistsOpt      { return func(f *FileExists) { f.gate = g } }

// NewFileExists constructs a FileExists scorer.
func NewFileExists(path string, opts ...FileExistsOpt) *FileExists {
	f := &FileExists{
		BaseScorer: NewBaseScorer("file_exists:"+path, 1.0, false),
		Path:       path,
	}
	f.RuleType = "file_exists"
	for _, o := range opts {
		o(f)
	}
	return f
}

// ToInvariant satisfies Scorer.
func (f *FileExists) ToInvariant() *Invariant {
	return &Invariant{
		Description: f.Path + " exists",
		Weight:      f.Weight(),
		Gate:        f.Gate(),
		Check:       map[string]interface{}{"type": "file_exists", "path": f.Path},
	}
}

// ToRule satisfies Scorer.
func (f *FileExists) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name:   f.Name(),
		Type:   f.RuleType,
		Config: map[string]interface{}{"path": f.Path},
	}
}

// ─── FileContains ───────────────────────────────────────────────────────

// FileContains checks file content against a substring or regex pattern.
type FileContains struct {
	BaseScorer
	Path        string
	Contains    string
	NotContains string
	Pattern     string
}

type FileContainsOpt func(*FileContains)

func FCContains(s string) FileContainsOpt    { return func(f *FileContains) { f.Contains = s } }
func FCNotContains(s string) FileContainsOpt { return func(f *FileContains) { f.NotContains = s } }
func FCPattern(p string) FileContainsOpt     { return func(f *FileContains) { f.Pattern = p } }
func FCWeight(w float64) FileContainsOpt     { return func(f *FileContains) { f.weight = w } }
func FCGate(g bool) FileContainsOpt          { return func(f *FileContains) { f.gate = g } }

// NewFileContains constructs a FileContains scorer.
func NewFileContains(path string, opts ...FileContainsOpt) *FileContains {
	f := &FileContains{
		BaseScorer: NewBaseScorer("file_content:"+path, 1.0, false),
		Path:       path,
	}
	f.RuleType = "file_content"
	for _, o := range opts {
		o(f)
	}
	return f
}

func (f *FileContains) ToInvariant() *Invariant {
	check := map[string]interface{}{"type": "file_content", "path": f.Path}
	if f.Contains != "" {
		check["contains"] = f.Contains
	}
	if f.NotContains != "" {
		check["not_contains"] = f.NotContains
	}
	if f.Pattern != "" {
		check["pattern"] = f.Pattern
	}
	return &Invariant{
		Description: f.Path + " content check",
		Weight:      f.Weight(),
		Gate:        f.Gate(),
		Check:       check,
	}
}

func (f *FileContains) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: f.Name(),
		Type: f.RuleType,
		Config: map[string]interface{}{
			"path":         f.Path,
			"contains":     f.Contains,
			"not_contains": f.NotContains,
			"pattern":      f.Pattern,
		},
	}
}

// ─── CommandExits ───────────────────────────────────────────────────────

// CommandExits checks that a shell command exits with the expected code.
type CommandExits struct {
	BaseScorer
	Command  string
	ExitCode int
}

type CommandExitsOpt func(*CommandExits)

func CEExitCode(code int) CommandExitsOpt { return func(c *CommandExits) { c.ExitCode = code } }
func CEWeight(w float64) CommandExitsOpt  { return func(c *CommandExits) { c.weight = w } }
func CEGate(g bool) CommandExitsOpt       { return func(c *CommandExits) { c.gate = g } }

// NewCommandExits constructs a CommandExits scorer.
func NewCommandExits(command string, opts ...CommandExitsOpt) *CommandExits {
	name := "command:"
	if len(command) > 30 {
		name += command[:30]
	} else {
		name += command
	}
	c := &CommandExits{
		BaseScorer: NewBaseScorer(name, 1.0, false),
		Command:    command,
	}
	c.RuleType = "command_exit"
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *CommandExits) ToInvariant() *Invariant {
	return &Invariant{
		Description: "command exits " + intToString(c.ExitCode),
		Weight:      c.Weight(),
		Gate:        c.Gate(),
		Check: map[string]interface{}{
			"type":      "command_exit",
			"command":   c.Command,
			"exit_code": c.ExitCode,
		},
	}
}

func (c *CommandExits) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: c.Name(),
		Type: c.RuleType,
		Config: map[string]interface{}{
			"command":   c.Command,
			"exit_code": c.ExitCode,
		},
	}
}

func intToString(n int) string {
	// small helper to avoid pulling in strconv just for this path
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// ─── SQLEquals ──────────────────────────────────────────────────────────

// SQLEquals runs a SQL query against a backing service and expects a
// specific result.
type SQLEquals struct {
	BaseScorer
	Service string
	Query   string
	Equals  interface{}
}

type SQLEqualsOpts struct {
	Service string
	Query   string
	Equals  interface{}
	Weight  float64
	Gate    bool
}

// NewSQLEquals constructs a SQLEquals scorer.
func NewSQLEquals(opts SQLEqualsOpts) *SQLEquals {
	weight := opts.Weight
	if weight == 0 {
		weight = 1.0
	}
	s := &SQLEquals{
		BaseScorer: NewBaseScorer("sql:"+opts.Service, weight, opts.Gate),
		Service:    opts.Service,
		Query:      opts.Query,
		Equals:     opts.Equals,
	}
	s.RuleType = "sql"
	return s
}

func (s *SQLEquals) ToInvariant() *Invariant {
	return &Invariant{
		Description: "SQL check on " + s.Service,
		Weight:      s.Weight(),
		Gate:        s.Gate(),
		Check: map[string]interface{}{
			"type":    "sql",
			"service": s.Service,
			"query":   s.Query,
			"equals":  s.Equals,
		},
	}
}

func (s *SQLEquals) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: s.Name(),
		Type: s.RuleType,
		Config: map[string]interface{}{
			"service": s.Service,
			"query":   s.Query,
			"equals":  s.Equals,
		},
	}
}

// ─── LLMJudge (generic spec-level invariant, kept for back-compat) ──────

// LLMJudge is the original, generic LLM-as-judge invariant scorer. For
// domain-specific scoring prefer the dedicated judges (Factuality, etc.)
// in scorer_llm_judge.go — they share the same server-side execution model
// with tuned prompts.
type LLMJudge struct {
	BaseScorer
	Criteria       string
	Model          string
	InputFrom      string
	Rubric         map[string]string
	Temperature    float64
	PromptTemplate string
}

type LLMJudgeOpt func(*LLMJudge)

func LJModel(m string) LLMJudgeOpt          { return func(j *LLMJudge) { j.Model = m } }
func LJInputFrom(s string) LLMJudgeOpt      { return func(j *LLMJudge) { j.InputFrom = s } }
func LJRubric(r map[string]string) LLMJudgeOpt {
	return func(j *LLMJudge) { j.Rubric = r }
}
func LJTemperature(t float64) LLMJudgeOpt   { return func(j *LLMJudge) { j.Temperature = t } }
func LJPromptTemplate(p string) LLMJudgeOpt { return func(j *LLMJudge) { j.PromptTemplate = p } }
func LJWeight(w float64) LLMJudgeOpt        { return func(j *LLMJudge) { j.weight = w } }
func LJGate(g bool) LLMJudgeOpt             { return func(j *LLMJudge) { j.gate = g } }

// NewLLMJudge constructs the generic judge scorer.
func NewLLMJudge(criteria string, opts ...LLMJudgeOpt) *LLMJudge {
	j := &LLMJudge{
		BaseScorer: NewBaseScorer("llm_judge", 1.0, false),
		Criteria:   criteria,
		Model:      "paragon-fast",
		InputFrom:  "workspace",
	}
	j.RuleType = "llm_as_judge"
	for _, o := range opts {
		o(j)
	}
	return j
}

func (j *LLMJudge) ToInvariant() *Invariant {
	check := map[string]interface{}{
		"type":        "llm_as_judge",
		"model":       j.Model,
		"criteria":    j.Criteria,
		"input_from":  j.InputFrom,
		"temperature": j.Temperature,
	}
	if len(j.Rubric) > 0 {
		check["rubric"] = j.Rubric
	}
	if j.PromptTemplate != "" {
		check["prompt_template"] = j.PromptTemplate
	}
	desc := j.Criteria
	if len(desc) > 80 {
		desc = desc[:80]
	}
	return &Invariant{Description: desc, Weight: j.Weight(), Gate: j.Gate(), Check: check}
}

func (j *LLMJudge) ToRule() *ScoreRulePayload {
	return &ScoreRulePayload{
		Name: j.Name(),
		Type: j.RuleType,
		Config: map[string]interface{}{
			"criteria":    j.Criteria,
			"model":       j.Model,
			"input_from":  j.InputFrom,
			"rubric":      j.Rubric,
			"temperature": j.Temperature,
		},
	}
}

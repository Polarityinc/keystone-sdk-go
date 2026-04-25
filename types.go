package keystone

import "time"

// APIError is returned for non-2xx HTTP responses.
type APIError struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "keystone: API error " + httpStatusText(e.StatusCode)
}

// ─── Sandbox ─────────────────────────────────────────────────────────────

// Sandbox represents a running sandbox environment.
type Sandbox struct {
	ID        string                 `json:"id"`
	SpecID    string                 `json:"spec_id"`
	State     string                 `json:"state"`
	Path      string                 `json:"path"`
	URL       string                 `json:"url"`
	CreatedAt time.Time              `json:"created_at"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
	Services  map[string]ServiceInfo `json:"services,omitempty"`
}

// ServiceInfo holds connection details for a running backing service.
type ServiceInfo struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Ready bool   `json:"ready"`
}

// CreateSandboxRequest is the payload for creating a sandbox.
type CreateSandboxRequest struct {
	SpecID   string            `json:"spec_id"`
	Timeout  int               `json:"timeout,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// Secrets forwarded from the caller's local environment. Merged on the
	// server with Dashboard-stored secrets for the billing owner; request
	// values win on collision. Typical use: pair with LoadDeclaredEnvSecrets
	// to auto-pull names listed in spec.secrets from os.Getenv.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// CommandRequest is the payload for running a command in a sandbox.
type CommandRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// CommandResult is the response from running a command.
type CommandResult struct {
	Command    string `json:"command"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int    `json:"duration_ms"`
}

// WriteFileRequest is the payload for writing a file into a sandbox.
type WriteFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// StateSnapshot represents the full state of a sandbox at a point in time.
type StateSnapshot struct {
	CapturedAt string                `json:"captured_at"`
	SandboxID  string                `json:"sandbox_id,omitempty"`
	Files      map[string]FileState  `json:"files,omitempty"`
	Processes  []ProcessInfo         `json:"processes,omitempty"`
	Metadata   map[string]string     `json:"metadata,omitempty"`
}

// FileState is metadata and a checksum for one file in a snapshot.
type FileState struct {
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Checksum string `json:"checksum"`
}

// ProcessInfo describes a running process inside a sandbox.
type ProcessInfo struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

// StateDiff represents changes in sandbox state since creation or last checkpoint.
type StateDiff struct {
	SandboxID string   `json:"sandbox_id,omitempty"`
	Added     []string `json:"added,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	Modified  []string `json:"modified,omitempty"`
}

// ─── Specs & Experiments ────────────────────────────────────────────────

// SandboxSpec represents a sandbox specification (uploaded as YAML).
type SandboxSpec struct {
	ID          string    `json:"id"`
	Version     int       `json:"version,omitempty"`
	Description string    `json:"description,omitempty"`
	Name        string    `json:"name,omitempty"`
	Content     string    `json:"content,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Experiment represents a test experiment.
type Experiment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SpecID    string    `json:"spec_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateExperimentRequest is the payload for creating an experiment.
type CreateExperimentRequest struct {
	Name    string            `json:"name"`
	SpecID  string            `json:"spec_id"`
	// Secrets forwarded from the caller's local environment. Applied to
	// every sandbox the experiment spins up. Merged server-side with
	// Dashboard secrets; request values win. Pair with
	// LoadDeclaredEnvSecrets to auto-pull names listed in spec.secrets.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// InvariantResult is a single invariant check outcome.
type InvariantResult struct {
	Name    string  `json:"name"`
	Passed  bool    `json:"passed"`
	Gate    bool    `json:"gate,omitempty"`
	Weight  float64 `json:"weight,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Message string  `json:"message,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

// ForbiddenCheckResult reports a trajectory constraint violation.
type ForbiddenCheckResult struct {
	Rule     string `json:"rule"`
	Violated bool   `json:"violated"`
	Details  string `json:"details,omitempty"`
}

// Reproducer is the minimal information needed to reproduce a failing scenario.
type Reproducer struct {
	SpecFile   string                 `json:"spec_file,omitempty"`
	Seed       int64                  `json:"seed,omitempty"`
	ScenarioID string                 `json:"scenario_id,omitempty"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
	Command    string                 `json:"command,omitempty"`
}

// ScenarioResult contains the result for a single scenario execution.
type ScenarioResult struct {
	ScenarioID      string                 `json:"scenario_id"`
	SandboxID       string                 `json:"sandbox_id,omitempty"`
	Status          string                 `json:"status"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
	WallMs          int64                  `json:"wall_ms,omitempty"`
	ExitCode        int                    `json:"exit_code,omitempty"`
	ToolCalls       int                    `json:"tool_calls,omitempty"`
	CompositeScore  float64                `json:"composite_score"`
	AgentOutput     string                 `json:"agent_output,omitempty"`
	AgentStderr     string                 `json:"agent_stderr,omitempty"`
	Invariants      []InvariantResult      `json:"invariants,omitempty"`
	ForbiddenChecks []ForbiddenCheckResult `json:"forbidden_checks,omitempty"`
	TraceFile       string                 `json:"trace_file,omitempty"`
	Reproducer      *Reproducer            `json:"reproducer,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Cost            *CostInfo              `json:"cost,omitempty"`
}

// RunMetrics are aggregate metrics across a full experiment run.
type RunMetrics struct {
	PassRate             float64 `json:"pass_rate"`
	MeanWallMs           int64   `json:"mean_wall_ms"`
	P95WallMs            int64   `json:"p95_wall_ms"`
	MeanToolCalls        float64 `json:"mean_tool_calls"`
	MeanTokens           int64   `json:"mean_tokens"`
	TotalCostUSD         float64 `json:"total_cost_usd"`
	MeanCostPerRunUSD    float64 `json:"mean_cost_per_run_usd"`
	ToolSuccessRate      float64 `json:"tool_success_rate"`
	SideEffectViolations int     `json:"side_effect_violations"`
}

// RunResults contains the top-level output of an experiment run.
type RunResults struct {
	RanAt          time.Time        `json:"ran_at"`
	SpecID         string           `json:"spec_id"`
	ExperimentID   string           `json:"experiment_id"`
	Seed           int64            `json:"seed,omitempty"`
	TotalScenarios int              `json:"total_scenarios"`
	Passed         int              `json:"passed"`
	Failed         int              `json:"failed"`
	Flaky          int              `json:"flaky,omitempty"`
	Errors         int              `json:"errors"`
	Metrics        RunMetrics       `json:"metrics"`
	Scenarios      []ScenarioResult `json:"scenarios,omitempty"`
}

// MetricComparison is a single metric compared across baseline and candidate runs.
type MetricComparison struct {
	Name      string  `json:"name"`
	Baseline  float64 `json:"baseline"`
	Candidate float64 `json:"candidate"`
	Delta     float64 `json:"delta"`
	Direction string  `json:"direction,omitempty"`
}

// Comparison is the response from comparing two experiments.
type Comparison struct {
	BaselineID  string             `json:"baseline_id"`
	CandidateID string             `json:"candidate_id"`
	Metrics     []MetricComparison `json:"metrics,omitempty"`
	Regressed   bool               `json:"regressed,omitempty"`
	Regressions []string           `json:"regressions,omitempty"`
	Summary     string             `json:"summary,omitempty"`
}

// ExperimentMetrics holds metrics for an experiment.
type ExperimentMetrics struct {
	ExperimentID string             `json:"experiment_id"`
	Metrics      map[string]float64 `json:"metrics,omitempty"`
}

// ─── Alerts / eval history ───────────────────────────────────────────────

// Alert represents an alert rule.
type Alert struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	EvalID       string `json:"eval_id,omitempty"`
	Condition    string `json:"condition"`
	Window       string `json:"window,omitempty"`
	Notify       string `json:"notify"`
	WebhookURL   string `json:"webhook_url,omitempty"`
	SlackChannel string `json:"slack_channel,omitempty"`
}

// CreateAlertRequest is the payload for creating an alert.
type CreateAlertRequest struct {
	Name         string `json:"name"`
	EvalID       string `json:"eval_id,omitempty"`
	Condition    string `json:"condition"`
	Notify       string `json:"notify"`
	WebhookURL   string `json:"webhook_url,omitempty"`
	SlackChannel string `json:"slack_channel,omitempty"`
}

// AlertFiring represents a triggered alert.
type AlertFiring struct {
	RuleID       string `json:"rule_id"`
	RuleName     string `json:"rule_name"`
	Reason       string `json:"reason"`
	ExperimentID string `json:"experiment_id,omitempty"`
	FiredAt      string `json:"fired_at"`
}

// EvalHistoryPoint is a single point in an eval's historical trend.
type EvalHistoryPoint struct {
	ExperimentID string  `json:"experiment_id"`
	PassRate     float64 `json:"pass_rate"`
	MeanWallMs   int64   `json:"mean_wall_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Ts           string  `json:"ts"`
}

// ─── Agents ──────────────────────────────────────────────────────────────

// AgentSnapshot is a versioned, content-addressed agent bundle.
type AgentSnapshot struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Version     int        `json:"version"`
	Tag         string     `json:"tag,omitempty"`
	Digest      string     `json:"digest"`
	SizeBytes   int64      `json:"size_bytes"`
	StoragePath string     `json:"storage_path,omitempty"`
	Runtime     string     `json:"runtime,omitempty"`
	Entrypoint  []string   `json:"entrypoint"`
	Auth        *AgentAuth `json:"auth,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AgentAuth declares what credentials an agent snapshot needs.
type AgentAuth struct {
	RequiredEnv []string            `json:"required_env,omitempty"`
	ConfigFiles []ConfigFile        `json:"config_files,omitempty"`
	Egress      map[string][]string `json:"egress,omitempty"`
}

// ConfigFile is a template-rendered file written into the sandbox.
type ConfigFile struct {
	Path     string `json:"path"`
	Template string `json:"template"`
}

// UploadSnapshotRequest is the metadata for uploading an agent snapshot.
type UploadSnapshotRequest struct {
	Name       string     `json:"name"`
	Tag        string     `json:"tag,omitempty"`
	Runtime    string     `json:"runtime,omitempty"`
	Entrypoint []string   `json:"entrypoint"`
	Auth       *AgentAuth `json:"auth,omitempty"`
}

// AgentPage is a paginated list of agent snapshots.
type AgentPage struct {
	Items      []*AgentSnapshot `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// ─── Datasets ────────────────────────────────────────────────────────────
//
// DatasetInfo and DatasetRecord live in datasets.go where the service is
// defined — kept there so the shape stays next to the calls that use it.

// ─── Traces ──────────────────────────────────────────────────────────────

// TraceEvent represents a single tool call or action captured during agent execution.
// Any agent can POST these to /v1/sandboxes/:id/trace to report what it did.
type TraceEvent struct {
	Timestamp    time.Time              `json:"ts"`
	EventType    string                 `json:"event_type,omitempty"`
	ToolName     string                 `json:"tool,omitempty"`
	Phase        string                 `json:"phase,omitempty"`
	DurationMs   int64                  `json:"duration_ms,omitempty"`
	Status       string                 `json:"status,omitempty"`
	ErrorType    string                 `json:"error_type,omitempty"`
	SpanID       string                 `json:"span_id,omitempty"`
	ParentSpanID string                 `json:"parent_span_id,omitempty"`
	RootSpanID   string                 `json:"root_span_id,omitempty"`
	Input        string                 `json:"input,omitempty"`
	Output       string                 `json:"output,omitempty"`
	Expected     string                 `json:"expected,omitempty"`
	InputBytes   int                    `json:"input_bytes,omitempty"`
	OutputBytes  int                    `json:"output_bytes,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Cost         *CostInfo              `json:"cost,omitempty"`
}

// CostInfo tracks token usage and cost for a trace event.
type CostInfo struct {
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens,omitempty"`
	ReasoningTokens int64   `json:"reasoning_tokens,omitempty"`
	Model           string  `json:"model"`
	EstimatedUSD    float64 `json:"estimated_usd"`
}

// TraceResponse is returned by GET /v1/sandboxes/:id/trace.
type TraceResponse struct {
	Events  []TraceEvent  `json:"events"`
	Metrics *TraceMetrics `json:"metrics"`
}

// TraceMetrics are aggregate metrics computed from trace events.
type TraceMetrics struct {
	TotalToolCalls  int                   `json:"total_tool_calls"`
	ToolSuccessRate float64               `json:"tool_success_rate"`
	MeanDurationMs  int64                 `json:"mean_duration_ms"`
	P95DurationMs   int64                 `json:"p95_duration_ms"`
	ToolBreakdown   map[string]ToolMetric `json:"tool_breakdown"`
}

// ToolMetric holds per-tool aggregated metrics.
type ToolMetric struct {
	Count     int     `json:"count"`
	MeanMs    int64   `json:"mean_ms"`
	ErrorRate float64 `json:"error_rate"`
}

// ─── Scorers ─────────────────────────────────────────────────────────────

// Score is a single score produced by a scorer. Mirrors polarity_keystone.Score.
type Score struct {
	Name     string                 `json:"name"`
	Score    float64                `json:"score"`
	Passed   bool                   `json:"passed"`
	Message  string                 `json:"message,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

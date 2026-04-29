package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Wrap returns an http.RoundTripper that intercepts LLM API responses
// and turns on full Keystone observability — the one-call default for
// "track everything." It does two things:
//
//  1. Wraps `base` with the same LLM-call interceptor as
//     WrapTransport (Anthropic /messages and OpenAI /chat/completions
//     are auto-reported to Keystone with full request/response, token
//     usage, tool calls, and latency).
//  2. Initialises a *TracingContext on the client so subsequent
//     Traced() calls emit start/end spans. The context is also
//     returned so callers can pin it for use later.
//
// SandboxID resolves from the explicit arg, then KEYSTONE_SANDBOX_ID,
// then "" (agent mode — events scoped to the API key server-side).
//
//	ks := keystone.NewClient(keystone.Config{})
//	transport, tc := ks.Wrap("", http.DefaultTransport)
//
//	anthropic := anthropic.NewClient(option.WithHTTPClient(&http.Client{
//	    Transport: transport,
//	}))
//	tc.Traced(ctx, "step", func() error { ... })
//
// For the most ergonomic shape (returning a struct with everything
// pre-wired), use Client.Observe instead.
func (c *Client) Wrap(sandboxID string, base http.RoundTripper) (http.RoundTripper, *TracingContext) {
	if base == nil {
		base = http.DefaultTransport
	}
	tc := c.InitTracing(sandboxID)
	transport := WrapTransport(c, sandboxID, base)
	return transport, tc
}

// keystoneTransport is an http.RoundTripper that intercepts LLM API responses,
// extracts tool calls and usage metadata, and reports them as trace events to
// Keystone. It is completely transparent to the caller — the original response
// is always returned unmodified, and reporting failures are silently ignored.
type keystoneTransport struct {
	client    *Client
	sandboxID string
	base      http.RoundTripper
}

// WrapTransport returns an http.RoundTripper that intercepts LLM API responses,
// extracts tool calls, and reports them to Keystone. Use it with any Go LLM SDK.
//
// Two modes, resolved in order:
//  1. Sandbox mode — explicit `sandboxID` arg or KEYSTONE_SANDBOX_ID env var.
//     Events POST to /v1/sandboxes/:id/trace and nest under the sandbox run.
//  2. Agent mode — no sandbox id, but the Client has an API key. Events POST
//     to /v1/traces and are scoped to the API key server-side. This is the
//     prod-observability path: any agent running in prod with a ks_live_ key
//     gets full LLM + tool traces tied to the billing owner.
//  3. Neither — returns `base` unchanged (silent no-op for local dev / CI).
//
// Usage — one line:
//
//	anthropicClient := anthropic.NewClient(option.WithHTTPClient(&http.Client{
//	    Transport: keystone.WrapTransport(ks, "", http.DefaultTransport),  // env auto-detected
//	}))
//
// Or for OpenAI:
//
//	openaiClient := openai.NewClient(option.WithHTTPClient(&http.Client{
//	    Transport: keystone.WrapTransport(ks, "", http.DefaultTransport),
//	}))
//
// The wrapper only intercepts POST requests to paths containing "/messages" or
// "/chat/completions". All other requests pass through unchanged. Trace event
// reporting happens in a background goroutine and never adds latency to the
// original request.
func WrapTransport(client *Client, sandboxID string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if sandboxID == "" {
		sandboxID = os.Getenv("KEYSTONE_SANDBOX_ID")
	}
	// Sandbox unset AND no API key to fall back to → nothing to report to.
	// Skip wrapping so we don't pay the parse-and-post cost for nothing.
	if sandboxID == "" && (client == nil || client.apiKey == "") {
		return base
	}
	return &keystoneTransport{
		client:    client,
		sandboxID: sandboxID, // may be "" — parseAndReport routes to /v1/traces in that case
		base:      base,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *keystoneTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only intercept POST requests to LLM API endpoints.
	if !t.shouldIntercept(req) {
		return t.base.RoundTrip(req)
	}

	// Capture request body (the prompt/messages).
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	start := time.Now()

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	latency := time.Since(start)

	// Read the response body so we can inspect it, then replace it so the
	// caller can still read it in full.
	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	if readErr != nil {
		return resp, nil
	}

	// Parse and report in a fire-and-forget goroutine.
	go t.parseAndReport(reqBody, respBody, latency)

	return resp, nil
}

// shouldIntercept returns true for POST requests whose path contains a known
// LLM API endpoint segment.
func (t *keystoneTransport) shouldIntercept(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	path := req.URL.Path
	return strings.Contains(path, "/messages") || strings.Contains(path, "/chat/completions")
}

// llmUsage covers both Anthropic and OpenAI token usage fields.
type llmUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

// parseAndReport extracts full input/output, tool calls, and usage from
// the request and response bodies, then sends trace events to Keystone.
func (t *keystoneTransport) parseAndReport(reqBody, respBody []byte, latency time.Duration) {
	var resp fullLLMResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return
	}

	now := time.Now()
	durationMs := latency.Milliseconds()
	spanID := fmt.Sprintf("span_%x", now.UnixNano())
	var events []TraceEvent

	// Normalize token counts.
	var inputTokens, outputTokens int64
	if resp.Usage != nil {
		inputTokens = resp.Usage.InputTokens
		outputTokens = resp.Usage.OutputTokens
		if inputTokens == 0 {
			inputTokens = resp.Usage.PromptTokens
		}
		if outputTokens == 0 {
			outputTokens = resp.Usage.CompletionTokens
		}
	}

	// Build output text from response.
	var outputParts []string
	for _, c := range resp.Content {
		if c.Type == "text" && c.Text != "" {
			outputParts = append(outputParts, c.Text)
		}
	}
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		if resp.Choices[0].Message.Content != "" {
			outputParts = append(outputParts, resp.Choices[0].Message.Content)
		}
	}
	outputText := strings.Join(outputParts, "")

	// LLM call event — full input (request body) and output (response text + tool calls).
	// The `metadata` block mirrors OpenTelemetry GenAI semantic conventions
	// (`gen_ai.*`) so traces exported via /otel/v1/traces round-trip cleanly
	// into OTel-native backends (Honeycomb, Tempo, etc.).
	provider := "unknown"
	if strings.Contains(string(respBody), `"content":[{"type":"tool_use"`) || strings.Contains(string(respBody), `"type":"text"`) {
		provider = "anthropic"
	} else if len(resp.Choices) > 0 {
		provider = "openai"
	}
	llmEvent := TraceEvent{
		Timestamp:   now,
		EventType:   "llm_call",
		Phase:       "complete",
		DurationMs:  durationMs,
		Status:      "ok",
		ToolName:    resp.Model,
		SpanID:      spanID,
		InputBytes:  len(reqBody),
		OutputBytes: len(respBody),
		Metadata: map[string]interface{}{
			"gen_ai.system":              provider,
			"gen_ai.request.model":       resp.Model,
			"gen_ai.response.model":      resp.Model,
			"gen_ai.usage.input_tokens":  inputTokens,
			"gen_ai.usage.output_tokens": outputTokens,
			"gen_ai.operation.name":      "chat",
		},
	}
	// Attach full input/output for observability.
	if len(reqBody) > 0 {
		llmEvent.Input = string(reqBody)
	}
	if outputText != "" {
		llmEvent.Output = outputText
	}
	if resp.Model != "" || inputTokens > 0 || outputTokens > 0 {
		llmEvent.Cost = &CostInfo{
			Model:        resp.Model,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			EstimatedUSD: EstimateCost(resp.Model, inputTokens, outputTokens, 0),
		}
	}
	events = append(events, llmEvent)

	// Tool call events — Anthropic format (with arguments).
	for _, c := range resp.Content {
		if c.Type == "tool_use" && c.Name != "" {
			inputJSON := ""
			if c.Input != nil {
				b, _ := json.Marshal(c.Input)
				inputJSON = string(b)
			}
			events = append(events, TraceEvent{
				Timestamp: now,
				EventType: "tool_use",
				ToolName:  c.Name,
				Phase:     "invoked",
				Status:    "ok",
				Input:     inputJSON,
			})
		}
	}

	// Tool call events — OpenAI format (with arguments).
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			events = append(events, TraceEvent{
				Timestamp: now,
				EventType: "tool_use",
				ToolName:  tc.Function.Name,
				Phase:     "invoked",
				Status:    "ok",
				Input:     tc.Function.Arguments,
			})
		}
	}

	if len(events) == 0 {
		return
	}

	// POST to Keystone.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload, _ := json.Marshal(map[string]any{"events": events})
	var traceURL string
	if t.sandboxID != "" {
		traceURL = t.client.baseURL + "/v1/sandboxes/" + url.PathEscape(t.sandboxID) + "/trace"
	} else {
		// Agent mode — server resolves api_key_id from Bearer token and
		// writes rows with sandbox_id = null.
		traceURL = t.client.baseURL + "/v1/traces"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, traceURL, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if t.client.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.client.apiKey)
	}

	traceResp, err := t.client.httpClient.Do(req)
	if err != nil {
		return
	}
	traceResp.Body.Close()
}

// fullLLMResponse extends llmResponse to capture text content and tool arguments.
type fullLLMResponse struct {
	Model   string              `json:"model"`
	Content []fullAnthropicBlock `json:"content"`
	Usage   *llmUsage           `json:"usage"`
	Choices []fullOpenAIChoice  `json:"choices"`
}

type fullAnthropicBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	ID    string `json:"id,omitempty"`
	Input any    `json:"input,omitempty"`
}

type fullOpenAIChoice struct {
	Message *fullOpenAIMessage `json:"message"`
}

type fullOpenAIMessage struct {
	Content   string            `json:"content"`
	ToolCalls []fullOpenAIToolCall `json:"tool_calls"`
}

type fullOpenAIToolCall struct {
	ID       string             `json:"id"`
	Function fullOpenAIFunction `json:"function"`
}

type fullOpenAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ─── RecordLLMCall — public manual emission ──────────────────────────────
//
// Mirrors TS `Keystone.recordLLMCall`. For callers that don't have an LLM
// SDK *value* whose transport / `.create()` we can wrap — multi-provider
// gateways, reverse proxies, custom HTTP routing layers — but already
// extract the same data WrapTransport pulls off the wire.
//
// Usage:
//
//	ks := keystone.NewClient(keystone.Config{})
//	start := time.Now()
//	resp, _ := http.Post(upstreamURL, "application/json", body)
//	// ... read resp.Body, parse usage ...
//	ks.RecordLLMCall(ctx, keystone.RecordLLMCallOpts{
//	    Provider:     "openrouter",
//	    Model:        "moonshotai/kimi-k2.6",
//	    InputTokens:  promptTokens,
//	    OutputTokens: completionTokens,
//	    DurationMs:   time.Since(start).Milliseconds(),
//	    InputMessages: requestBodyMessages,
//	    OutputText:   assistantText,
//	})

// RecordToolCall describes a single tool the model invoked. Lifted from
// the on-the-wire response — the SDK doesn't run the tool, just records
// that it was requested.
type RecordToolCall struct {
	Name      string
	ID        string
	Arguments string
}

// RecordLLMCallOpts is the input to Client.RecordLLMCall. Mirrors the TS
// `RecordLLMCallOpts` shape so the two SDKs emit identical event payloads.
type RecordLLMCallOpts struct {
	// Provider label, e.g. "anthropic", "openai", "openrouter", "gemini",
	// "xai". Surfaces in the event's `tool` field (`<provider>.create`)
	// and in OTel `gen_ai.system`. Free-form.
	Provider string
	// Resolved model identifier as the upstream provider sees it. Lands
	// in `cost.model` and `gen_ai.response.model`.
	Model string
	// Original model the client requested, when it differs from the
	// resolved upstream model (e.g. paragon-fast → kimi-k2.6 after
	// gateway routing). Defaults to Model. Sets `gen_ai.request.model`.
	RequestedModel string
	InputTokens    int64
	OutputTokens   int64
	CacheTokens    int64 // 0 = unknown/unused
	ReasoningTokens int64
	DurationMs     int64
	// Raw input — typically the request body's `messages`. JSON-marshaled
	// and truncated to ~4KB before being attached to the event.
	InputMessages any
	// Aggregated assistant text (sum of streamed deltas if applicable).
	OutputText string
	// Tool calls the model invoked. Emitted as child `tool_use` events.
	ToolCalls []RecordToolCall
	// "ok" (default) or "error" for upstream failures / timeouts.
	Status string
	// Free-form error tag (e.g. "upstream_5xx", "timeout").
	ErrorType string
	// Custom OTel-style attributes merged into the llm_call event metadata
	// — e.g. `gen_ai.proxy.feature`. Round-trips through OTel exports.
	Metadata map[string]interface{}
	// Override the auto-generated llm_call span id. Pass a request id from
	// upstream to correlate client + gateway spans into one trace tree.
	SpanID string
	// Link this llm_call under a parent span (an outer agent step).
	ParentSpanID string
	// Override sandbox routing for just this event:
	//   nil  → use KEYSTONE_SANDBOX_ID env (sandbox mode if set)
	//   ""   → force agent mode (POSTs to /v1/traces)
	//   "id" → force sandbox mode (POSTs to /v1/sandboxes/id/trace)
	SandboxIDOverride *string
}

// RecordLLMCall emits an `llm_call` event (plus child `tool_use` events for
// any tool calls in opts.ToolCalls) with the same shape WrapTransport
// produces. Synchronous — returns when the POST settles. Use a goroutine if
// you don't want to block on Keystone latency.
//
// Returns an error only on POST failure. Constructs no traces on success.
func (c *Client) RecordLLMCall(ctx context.Context, opts RecordLLMCallOpts) error {
	now := time.Now()
	llmSpanID := opts.SpanID
	if llmSpanID == "" {
		llmSpanID = fmt.Sprintf("span_%x", now.UnixNano())
	}

	// Marshal + truncate input messages.
	inputStr := ""
	if opts.InputMessages != nil {
		if s, ok := opts.InputMessages.(string); ok {
			inputStr = s
		} else {
			b, err := json.Marshal(opts.InputMessages)
			if err == nil {
				inputStr = string(b)
			}
		}
	}
	if len(inputStr) > 4000 {
		inputStr = inputStr[:4000]
	}

	// Build output: assistant text, then a JSON-encoded tool_calls summary.
	output := opts.OutputText
	if len(opts.ToolCalls) > 0 {
		toolSummary := make([]map[string]string, 0, len(opts.ToolCalls))
		for _, tc := range opts.ToolCalls {
			toolSummary = append(toolSummary, map[string]string{
				"tool": tc.Name,
				"args": tc.Arguments,
			})
		}
		if b, err := json.Marshal(map[string]any{"tool_calls": toolSummary}); err == nil {
			if output != "" {
				output += "\n"
			}
			output += string(b)
		}
	}
	if len(output) > 4000 {
		output = output[:4000]
	}

	status := opts.Status
	if status == "" {
		status = "ok"
	}
	requestedModel := opts.RequestedModel
	if requestedModel == "" {
		requestedModel = opts.Model
	}

	metadata := map[string]interface{}{
		"gen_ai.system":              opts.Provider,
		"gen_ai.request.model":       requestedModel,
		"gen_ai.response.model":      opts.Model,
		"gen_ai.usage.input_tokens":  opts.InputTokens,
		"gen_ai.usage.output_tokens": opts.OutputTokens,
		"gen_ai.operation.name":      "chat",
	}
	for k, v := range opts.Metadata {
		metadata[k] = v
	}

	llmEvent := TraceEvent{
		Timestamp:    now,
		EventType:    "llm_call",
		ToolName:     opts.Provider + ".create",
		Phase:        "complete",
		DurationMs:   opts.DurationMs,
		Status:       status,
		ErrorType:    opts.ErrorType,
		SpanID:       llmSpanID,
		ParentSpanID: opts.ParentSpanID,
		Input:        inputStr,
		Output:       output,
		Metadata:     metadata,
		Cost: &CostInfo{
			Model:           opts.Model,
			InputTokens:     opts.InputTokens,
			OutputTokens:    opts.OutputTokens,
			CacheReadTokens: opts.CacheTokens,
			ReasoningTokens: opts.ReasoningTokens,
			EstimatedUSD:    EstimateCost(opts.Model, opts.InputTokens, opts.OutputTokens, opts.CacheTokens),
		},
	}

	events := []TraceEvent{llmEvent}
	for _, tc := range opts.ToolCalls {
		events = append(events, TraceEvent{
			Timestamp:    now,
			EventType:    "tool_use",
			ToolName:     firstNonEmpty(tc.Name, "unknown"),
			Phase:        "invoked",
			Status:       "ok",
			SpanID:       fmt.Sprintf("span_%x", time.Now().UnixNano()),
			ParentSpanID: llmSpanID,
			Input:        tc.Arguments,
		})
	}

	// Resolve sandbox id. Explicit override wins; nil → env.
	sandboxID := os.Getenv("KEYSTONE_SANDBOX_ID")
	if opts.SandboxIDOverride != nil {
		sandboxID = *opts.SandboxIDOverride
	}

	path := "/v1/traces"
	if sandboxID != "" {
		path = "/v1/sandboxes/" + url.PathEscape(sandboxID) + "/trace"
	}

	_, err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"events": events})
	return err
}

// firstNonEmpty returns the first non-empty string in vals, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

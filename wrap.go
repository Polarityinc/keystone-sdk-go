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
// The sandbox id is optional — resolution order matches the Python + TS SDKs:
//  1. explicit argument
//  2. KEYSTONE_SANDBOX_ID env var (Keystone injects this into sandboxed agent processes)
//  3. neither → WrapTransport returns `base` unchanged (no-op, safe for local dev / CI)
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
	// Resolution mirrors Python/TS (wrap): explicit arg → KEYSTONE_SANDBOX_ID
	// env → no-op. Returning `base` unchanged when neither source yields an
	// id keeps local-dev / CI callers from paying the trace-reporting cost.
	if sandboxID == "" {
		sandboxID = os.Getenv("KEYSTONE_SANDBOX_ID")
	}
	if sandboxID == "" {
		return base
	}
	return &keystoneTransport{
		client:    client,
		sandboxID: sandboxID,
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
	traceURL := t.client.baseURL + "/v1/sandboxes/" + url.PathEscape(t.sandboxID) + "/trace"
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

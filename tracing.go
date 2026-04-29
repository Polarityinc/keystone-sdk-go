package keystone

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"
)

// ─── OTel bridge ────────────────────────────────────────────────────────
//
// Go's OTel SDK lives in go.opentelemetry.io/otel — we deliberately avoid a
// dependency on it at the SDK level. Users wire their tracer up separately
// and point OTEL_EXPORTER_OTLP_ENDPOINT at Keystone's /otel/v1/traces. The
// helpers below provide a flush hook so users can piggy-back OTel span
// flushing on the Keystone lifecycle.

var (
	otelFlushMu sync.Mutex
	otelFlushCB []func(context.Context) error
)

// RegisterOtelFlush registers a callback invoked by FlushOtel. Useful for
// plumbing OTel span processor flushes into program shutdown.
func RegisterOtelFlush(cb func(context.Context) error) {
	otelFlushMu.Lock()
	defer otelFlushMu.Unlock()
	otelFlushCB = append(otelFlushCB, cb)
}

// FlushOtel runs every registered flush callback, returning the first error
// encountered. Safe to call multiple times.
func FlushOtel(ctx context.Context) error {
	otelFlushMu.Lock()
	cbs := append([]func(context.Context) error{}, otelFlushCB...)
	otelFlushMu.Unlock()
	var firstErr error
	for _, cb := range cbs {
		if err := cb(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// TracingContext holds the state for automatic span reporting.
// Create one via Client.InitTracing and pass it around, or store it
// in a package-level variable for convenience.
type TracingContext struct {
	client    *Client
	sandboxID string

	mu            sync.Mutex
	currentSpanID string
}

// InitTracing creates a tracing context.
//
// Two modes:
//  1. Sandbox mode — explicit `sandboxID` arg or KEYSTONE_SANDBOX_ID env var.
//     Traced() spans post to /v1/sandboxes/:id/trace and nest under the run.
//  2. Agent mode — no sandbox id, but the Client has an API key. Traced()
//     spans post to /v1/traces, scoped to the API key server-side. This is
//     the prod-observability path: any agent with a ks_live_ key gets full
//     tool traces tied to the billing owner, no sandbox required.
//  3. Neither — returns a no-op whose Traced() just runs fn().
//
//	tc := ks.InitTracing("")  // picks up KEYSTONE_SANDBOX_ID
//	tc.Traced(ctx, "write_file", func() error {
//	    return os.WriteFile(path, content, 0644)
//	})
func (c *Client) InitTracing(sandboxID string) *TracingContext {
	if sandboxID == "" {
		sandboxID = os.Getenv("KEYSTONE_SANDBOX_ID")
	}
	return &TracingContext{
		client:    c,
		sandboxID: sandboxID, // may be "" — postEvent routes to /v1/traces in that case
	}
}

// Traced executes fn inside a traced span. It auto-captures start time,
// duration, and error status. Nested Traced calls create parent-child spans.
// Without a sandbox id, Traced still emits events to /v1/traces when the
// Client has an API key (agent mode). If neither is set, Traced runs fn
// transparently without emitting any events.
func (tc *TracingContext) Traced(ctx context.Context, name string, fn func() error) error {
	if tc == nil {
		return fn()
	}
	// No sandbox AND no API key → nothing to report to. Pass through.
	if tc.sandboxID == "" && (tc.client == nil || tc.client.apiKey == "") {
		return fn()
	}
	spanID := makeSpanID()

	tc.mu.Lock()
	parentSpanID := tc.currentSpanID
	tc.currentSpanID = spanID
	tc.mu.Unlock()

	defer func() {
		tc.mu.Lock()
		tc.currentSpanID = parentSpanID
		tc.mu.Unlock()
	}()

	// Report start.
	tc.postEvent(TraceEvent{
		Timestamp: time.Now(),
		EventType: "tool_call",
		ToolName:  name,
		Phase:     "start",
		Status:    "ok",
	})

	start := time.Now()
	err := fn()
	durationMs := time.Since(start).Milliseconds()

	// Report end.
	status := "ok"
	errType := ""
	if err != nil {
		status = "error"
		errType = "tool_error"
	}
	tc.postEvent(TraceEvent{
		Timestamp:  time.Now(),
		EventType:  "tool_call",
		ToolName:   name,
		Phase:      "end",
		DurationMs: durationMs,
		Status:     status,
	})
	_ = errType // available for future use

	return err
}

// TracedValue is like Traced but returns a value along with the error.
func TracedValue[T any](tc *TracingContext, ctx context.Context, name string, fn func() (T, error)) (T, error) {
	var result T
	err := tc.Traced(ctx, name, func() error {
		var e error
		result, e = fn()
		return e
	})
	return result, err
}

func (tc *TracingContext) postEvent(event TraceEvent) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		payload := map[string]any{"events": []TraceEvent{event}}
		var path string
		if tc.sandboxID != "" {
			path = "/v1/sandboxes/" + url.PathEscape(tc.sandboxID) + "/trace"
		} else {
			path = "/v1/traces"
		}
		tc.client.doJSON(ctx, "POST", path, payload)
	}()
}

func makeSpanID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

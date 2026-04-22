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

// InitTracing creates a tracing context for a sandbox.
//
// Resolution order for the sandbox id:
//  1. explicit argument
//  2. KEYSTONE_SANDBOX_ID env var (Keystone injects this when your agent
//     runs inside a sandbox — you shouldn't set it manually)
//  3. neither → returns a no-op TracingContext whose Traced() passes
//     through without emitting any trace events. Matches the Python + TS
//     SDKs' "wrap does nothing outside a sandbox" behavior.
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
		sandboxID: sandboxID,
	}
}

// Traced executes fn inside a traced span. It auto-captures start time,
// duration, and error status. Nested Traced calls create parent-child spans.
// When the TracingContext has no sandbox id (outside a Keystone sandbox),
// Traced runs fn transparently without emitting any events.
func (tc *TracingContext) Traced(ctx context.Context, name string, fn func() error) error {
	if tc == nil || tc.sandboxID == "" {
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
		tc.client.doJSON(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(tc.sandboxID)+"/trace", payload)
	}()
}

func makeSpanID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

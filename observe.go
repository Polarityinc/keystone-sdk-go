package keystone

import (
	"net/http"
	"os"
)

// ObserveOptions configures the one-call observability bootstrap.
//
// Go SDK doesn't wrap concrete LLM clients directly (every Go LLM SDK
// builds on http.Client); instead Observe returns an http.RoundTripper
// callers attach to whichever LLM SDK they use, plus an optional
// TracingContext for @-style span reporting.
type ObserveOptions struct {
	// SandboxID scopes events to a sandbox run. Falls back to
	// KEYSTONE_SANDBOX_ID; empty means agent mode (events post to
	// /v1/traces, scoped by API key server-side).
	SandboxID string

	// Tracing enables InitTracing() — a TracingContext for Traced()
	// span reporting is returned in ObserveResult.Tracing. Defaults
	// to true if zero; set DisableTracing to opt out.
	DisableTracing bool

	// BaseTransport is the http.RoundTripper to wrap. Defaults to
	// http.DefaultTransport.
	BaseTransport http.RoundTripper
}

// ObserveResult bundles the wiring returned by Client.Observe.
type ObserveResult struct {
	// Transport is an http.RoundTripper that intercepts LLM API
	// responses and reports trace events to Keystone. Plug it into
	// any Go LLM SDK that accepts a custom http.Client.
	Transport http.RoundTripper

	// Tracing is non-nil when tracing is enabled. Use Tracing.Traced
	// to wrap functions for span reporting, or pass it to TracedValue
	// for typed return values.
	Tracing *TracingContext

	// Applied lists the instrumentation layers that were wired up,
	// e.g. ["transport", "tracing"]. Useful for startup logging.
	Applied []string
}

// Observe is a one-call observability bootstrap. It wires up an LLM
// HTTP transport and a tracing context in a single step, so callers
// don't have to remember to call WrapTransport + InitTracing
// separately.
//
//	ks := keystone.NewClient(keystone.Config{})
//	obs := ks.Observe(keystone.ObserveOptions{})
//
//	// Plug the transport into the Anthropic client:
//	anthropicClient := anthropic.NewClient(option.WithHTTPClient(&http.Client{
//	    Transport: obs.Transport,
//	}))
//
//	// Use the tracing context for in-process spans:
//	obs.Tracing.Traced(ctx, "write_file", func() error {
//	    return os.WriteFile(path, content, 0644)
//	})
//
// SandboxID falls back to KEYSTONE_SANDBOX_ID. When neither is set and
// the Client has no API key, Transport is the unwrapped base and
// Tracing is a no-op — same fail-soft behaviour as the underlying
// WrapTransport / InitTracing helpers.
func (c *Client) Observe(opts ObserveOptions) ObserveResult {
	sandboxID := opts.SandboxID
	if sandboxID == "" {
		sandboxID = os.Getenv("KEYSTONE_SANDBOX_ID")
	}
	base := opts.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}

	res := ObserveResult{
		Transport: WrapTransport(c, sandboxID, base),
	}
	if res.Transport != base {
		res.Applied = append(res.Applied, "transport")
	}

	if !opts.DisableTracing {
		res.Tracing = c.InitTracing(sandboxID)
		// Mark tracing applied only when the context will actually
		// emit (sandbox set OR API key set). Mirrors the no-op
		// short-circuit in TracingContext.Traced.
		if sandboxID != "" || c.apiKey != "" {
			res.Applied = append(res.Applied, "tracing")
		}
	}

	return res
}

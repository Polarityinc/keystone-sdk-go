# Keystone SDK for Go

Go client for the [Keystone](https://keystone.polarity.cc) agent evaluation
+ sandboxed-execution platform. Shares a single source of truth with the
[TypeScript](https://github.com/Polarityinc/keystone-sdk-js) and
[Python](https://github.com/Polarityinc/keystone-sdk-python) SDKs —
byte-identical cost estimates and prompt rendering across all three.

## Install

```bash
go get github.com/Polarityinc/keystone-sdk-go
```

## 60-second quick start: `Eval()`

The shortest path from "I have an agent" to "I have an evaluation":

```go
import (
    keystone "github.com/Polarityinc/keystone-sdk-go"
)

result, err := keystone.Eval(ctx, "summarisation-quality", keystone.EvalConfig{
    Data: []keystone.EvalRow{
        {Input: "Long article about whales...", Expected: "Whales are mammals."},
        {Input: "Article about Java GC...",     Expected: "Java GC reclaims memory."},
    },
    Task: func(ctx context.Context, input any) (any, error) {
        return myAgent(input.(string))
    },
    Scores: []keystone.Scorer{
        keystone.NewFactuality(keystone.JudgeModel("paragon-fast")),
        keystone.NewAnswerRelevancy(nil),
    },
    MaxConcurrency: 4,
})

fmt.Println(result.Summary) // p50/p95/mean per scorer
```

If `KEYSTONE_API_KEY` is set, the run is also recorded to your dashboard;
otherwise it stays purely local. Same shape in TypeScript and Python.

## Sandbox-as-a-tool ergonomics

`Create` / `Get` / `List` / `Handle` return a bound `*SandboxHandle` so an
agent loop can call the sandbox without threading the ID:

```go
sb, err := ks.Sandboxes.Create(ctx, keystone.CreateSandboxRequest{SpecID: "spec-123"})
if err != nil { return err }

_, _ = sb.Exec(ctx, "python script.py")
_  = sb.Write(ctx, "/tmp/input.json", payload)
out, _ := sb.Read(ctx, "/tmp/output.json")
diff, _ := sb.Diff(ctx)

_ = sb.Destroy(ctx)
```

Same pattern on `*ExperimentHandle` and `*AgentSnapshotHandle`:

```go
exp, _ := ks.Experiments.Create(ctx, keystone.CreateExperimentRequest{
    Name: "nightly", SpecID: "s",
})
results, _ := exp.RunAndWait(ctx, keystone.RunAndWaitOpts{
    Scores: []keystone.Scorer{ /* ... */ },
})
cmp, _ := exp.Compare(ctx, otherExp)
m, _   := exp.Metrics(ctx)

snap, _ := ks.Agents.Upload(ctx, req, bundleReader)
_ = snap.Delete(ctx)
```

Handles embed `*Sandbox` / `*Experiment` / `*AgentSnapshot`, so reading
`sb.ID`, `exp.Status`, `snap.Version` keeps working unchanged. The old
service-level methods (`ks.Sandboxes.RunCommand(ctx, id, …)`, etc.) stay
too — handle methods just delegate.

> Note: on `*SandboxHandle`, the state-snapshot accessor is `GetState(ctx)`
> — `State()` would shadow the embedded `Sandbox.State` string field.

## Auto-instrument LLM HTTP traffic

Go can't monkey-patch like Python/TS, but instrumentation is a one-liner —
swap the `http.Client.Transport` and every OpenAI / Anthropic / Mistral /
Google GenAI / xAI / Groq / Together call you make through it auto-traces:

```go
httpClient := &http.Client{
    Transport: keystone.WrapTransport(ks, os.Getenv("KEYSTONE_SANDBOX_ID"), http.DefaultTransport),
}
// Hand `httpClient` to your OpenAI / Anthropic SDK and you're done.
```

## Manual tracing when you want it

```go
tc := ks.InitTracing(os.Getenv("KEYSTONE_SANDBOX_ID"))   // returns *TracingContext

err := tc.Traced(ctx, "embed-doc", func() error {
    _, err := openai.Embeddings.Create(ctx, params)
    return err
})

// Generic typed variant for fns that return a value:
val, err := keystone.TracedValue(tc, ctx, "fetch", func() (User, error) {
    return db.Users.Find(uid)
})
```

Spans automatically nest via `context.Context` propagation.

## Multi-provider gateways / proxies — `RecordLLMCall`

`WrapTransport` intercepts an `http.Client` you hand to an LLM SDK. If your
code is a gateway / proxy / custom routing layer making raw `http.Post`
calls — switching across Anthropic, OpenAI, OpenRouter, Gemini, etc. per
request — there's no transport-bound SDK client to wrap. Use
`Client.RecordLLMCall(ctx, opts)` to emit the same `llm_call` event shape
`WrapTransport` produces:

```go
ks := keystone.NewClient(keystone.Config{})

// Inside your gateway handler, after the upstream call settles:
start := time.Now()
resp, _ := http.Post(upstreamURL, "application/json", body)
var parsed responseShape
_ = json.NewDecoder(resp.Body).Decode(&parsed)

err := ks.RecordLLMCall(ctx, keystone.RecordLLMCallOpts{
    Provider:       "openrouter",                  // free-form label
    Model:          parsed.Model,                  // resolved upstream model
    RequestedModel: req.Model,                     // what the caller asked for
    InputTokens:    parsed.Usage.PromptTokens,
    OutputTokens:   parsed.Usage.CompletionTokens,
    DurationMs:     time.Since(start).Milliseconds(),
    InputMessages:  req.Messages,                  // truncated to ~4KB on the wire
    OutputText:     parsed.Choices[0].Message.Content,
    ToolCalls: []keystone.RecordToolCall{
        {Name: tc.Name, ID: tc.ID, Arguments: tc.Args},
    },
    Metadata: map[string]interface{}{
        "gen_ai.proxy.fell_back": false,
    },
})
```

Same on-the-wire shape as `WrapTransport`-emitted events, so traces from a
gateway and from a transport-wrapped SDK land in the dashboard with
identical schema. Sandbox routing follows the same rules as
`WrapTransport`: explicit `SandboxIDOverride` → `KEYSTONE_SANDBOX_ID` env →
agent mode (`/v1/traces`).

Synchronous — returns when the POST settles. Run in a goroutine if you
don't want to block on Keystone latency.

## What's in the SDK

- **9 service surfaces** — `Sandboxes`, `Specs`, `Experiments`, `Alerts`, `Agents`, `Datasets`, `Scoring`, `Export`, `Prompts`
- **3 bound handles** — `*SandboxHandle`, `*ExperimentHandle`, `*AgentSnapshotHandle` with delegated methods
- **29 built-in scorers** across 5 families (Heuristic, LLM-judge, RAG, Embedding, Sandbox)
- **`Eval(ctx, name, EvalConfig)`** — Braintrust-style one-call eval primitive
- **`TracingContext.Traced(ctx, name, fn)`** + `TracedValue[T]` for manual span control
- **`WrapTransport(client, sandboxID, baseTransport)`** — drop-in `http.RoundTripper` that intercepts LLM API calls and reports prompts, tokens, latency to the dashboard
- **`Client.RecordLLMCall(ctx, opts)`** — gateway/proxy entry point: emit `llm_call` events without a transport-wrappable SDK client
- **Prompt management** — `ks.Prompts.Create/Get/List/Delete`, `Prompt.Render(vars)`
- **Bulk export** — paginated trace/span/scenario/score iteration, JSON or NDJSON experiment dumps

## License

MIT.

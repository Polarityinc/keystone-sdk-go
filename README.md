# Keystone SDK for Go

Go client for the [Keystone](https://keystone.polarity.cc) agent evaluation +
sandboxed execution platform. Ships alongside the
[Python](https://github.com/Polarityinc/keystone-sdk-python) and
[TypeScript](https://github.com/Polarityinc/keystone-sdk-js) SDKs at byte-for-byte
feature parity.

## Install

```bash
go get github.com/Polarityinc/keystone-sdk-go@latest
```

```go
import keystone "github.com/Polarityinc/keystone-sdk-go"
```

## Quick start

```go
package main

import (
    "context"
    "fmt"

    keystone "github.com/Polarityinc/keystone-sdk-go"
)

func main() {
    ctx := context.Background()
    ks := keystone.NewClient(keystone.Config{APIKey: "ks_live_..."})

    // Create an experiment and run it with three client-side scorers.
    exp, _ := ks.Experiments.Create(ctx, keystone.CreateExperimentRequest{
        Name: "nightly-regression", SpecID: "spec-123",
    })

    results, err := ks.Experiments.RunAndWait(ctx, exp.ID, keystone.RunAndWaitOpts{
        Scores: []keystone.Scorer{
            keystone.NewFactuality(keystone.JudgeModel("paragon-fast")),
            keystone.NewExactMatch(keystone.EMExpectedKey("expected"), keystone.EMCaseSensitive(false)),
            keystone.NewFileExists("output.txt", keystone.FEGate(true)),
        },
    })
    if err != nil {
        panic(err)
    }

    fmt.Printf("pass rate: %.0f%%\n", results.Metrics.PassRate*100)

    // Stream every trace for the experiment.
    for trace := range ks.Export.Traces(ctx, keystone.TraceFilter{ExperimentID: exp.ID}, 100) {
        fmt.Println(trace["tool"], trace["cost"])
    }
}
```

## What's in the SDK

| Area | Symbols |
|---|---|
| **Client services** | `Sandboxes`, `Specs`, `Experiments`, `Alerts`, `Agents`, `Datasets`, `Scoring`, `Export`, `Prompts` |
| **28 built-in scorers** | Factuality · Battle · ClosedQA · Humor · Moderation · Summarization · SQLJudge · Translation · Security · ContextPrecision · ContextRecall · ContextRelevancy · ContextEntityRecall · Faithfulness · AnswerRelevancy · AnswerSimilarity · AnswerCorrectness · ExactMatch · Levenshtein · NumericDiff · JSONDiff · JSONValidity · SemanticListContains · EmbeddingSimilarity · FileExists · FileContains · CommandExits · SQLEquals · LLMJudge |
| **Tracing** | `WrapTransport(client, sandboxID, base)` · `InitTracing(sandboxID).Traced(ctx, name, fn)` · `TracedValue[T]` |
| **OTel bridge** | `RegisterOtelFlush(cb)` · `FlushOtel(ctx)` · `gen_ai.*` metadata on LLM trace events |
| **Prompt mgmt** | `ks.Prompts.Create/Get/List/Delete`, `RenderTemplate(template, vars)`, mustache-lite syntax matching Python & TS |
| **Export** | `ks.Export.Traces/Spans/Scenarios/Scores(ctx, filter, pageSize)` returning channels; `ks.Export.Experiment(ctx, id, format)` for full dumps |
| **Pricing** | `EstimateCost(model, in, out, cache)` across 82 models (sync'd from the shared `pricing.json` SSOT) |

## Custom scorers

```go
myScorer := keystone.NewScorer(
    func(ctx context.Context, s keystone.ScenarioResult) (any, error) {
        return strings.Contains(s.AgentOutput, "ok"), nil
    },
    keystone.CustomName("contains-ok"),
    keystone.CustomWeight(0.5),
)
```

## Feature parity

All three SDKs share the same `pricing.json` source of truth and a byte-identical
mustache-lite renderer — cost estimates and prompt rendering agree across Go,
Python, and TypeScript runtime outputs. See
[keystone-sdk-js](https://github.com/Polarityinc/keystone-sdk-js) and
[keystone-sdk-python](https://github.com/Polarityinc/keystone-sdk-python) for
the sibling implementations.

## Versioning

Semver. `v0.x` = alpha/beta; `v1.0` cuts when the API shape is frozen.

## License

MIT.

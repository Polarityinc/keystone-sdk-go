package keystone

// Curated scorer presets for common agent shapes.
//
// The 28 built-in scorers are organised across 5 families. Picking
// the right ones for your eval is a paradox-of-choice problem on day
// one — these presets are opinionated bundles that match the most
// common agent shapes. They return plain []Scorer slices so callers
// can append extras.
//
// Three presets:
//   * PresetChat            — general conversational quality
//   * PresetRAG             — retrieval-augmented generation
//   * PresetAgentWithTools  — agent that calls tools
//
// Every preset accepts a model name threaded into the LLM-judge
// scorers. Pass an empty string for the default ("paragon-fast").

const presetDefaultModel = "paragon-fast"

func presetModelOpts(model string) []JudgeOpt {
	if model == "" {
		model = presetDefaultModel
	}
	return []JudgeOpt{JudgeModel(model)}
}

// PresetChat returns scorers for general chat agents:
//
//   - Factuality       — does the answer match the ground truth?
//   - AnswerRelevancy  — does the answer address the question?
//   - Moderation       — is the response free of harmful content?
//
// Pass an empty string for model to use the default (paragon-fast).
func PresetChat(model string) []Scorer {
	opts := presetModelOpts(model)
	return []Scorer{
		NewFactuality(opts...),
		NewAnswerRelevancy(nil, opts...),
		NewModeration(opts...),
	}
}

// PresetRAG returns scorers for retrieval-augmented generation
// pipelines:
//
//   - ContextPrecision  — are the retrieved chunks relevant?
//   - ContextRecall     — do the retrieved chunks contain enough info?
//   - Faithfulness      — is the answer grounded in the retrieved context?
//   - AnswerRelevancy   — does the answer address the question?
//
// Pass an empty string for model to use the default (paragon-fast).
func PresetRAG(model string) []Scorer {
	opts := presetModelOpts(model)
	return []Scorer{
		NewContextPrecision(nil, opts...),
		NewContextRecall(nil, opts...),
		NewFaithfulness(nil, opts...),
		NewAnswerRelevancy(nil, opts...),
	}
}

// PresetAgentWithTools returns scorers for agents that call tools:
//
//   - JSONValidity     — every tool-call argument blob parses as JSON.
//   - Factuality       — does the final answer match the ground truth?
//   - AnswerRelevancy  — does the final answer address the user's request?
//
// Pass an empty string for model to use the default (paragon-fast).
func PresetAgentWithTools(model string) []Scorer {
	opts := presetModelOpts(model)
	return []Scorer{
		NewJSONValidity(),
		NewFactuality(opts...),
		NewAnswerRelevancy(nil, opts...),
	}
}

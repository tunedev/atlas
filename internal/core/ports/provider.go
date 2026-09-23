package ports

import (
	"context"
	"time"
)

// Alternative is one token the model could have produced at a position, with
// the log probability it assigned.
type Alternative struct {
	Text    string
	LogProb float64
}

// Token is one position in a completion. Alternatives is empty unless the
// caller asked for them.
//
// On both of this port's current implementations (Ollama and vLLM), LogProb
// and Alternatives describe the model's distribution before any schema
// constrained the output, so the token actually produced is not always the
// one with the highest probability here. The port itself does not guarantee
// this; it is how these two engines behave, not a contract every Provider
// must honor.
type Token struct {
	Text         string
	LogProb      float64
	Alternatives []Alternative
}

// Usage is what the call cost, in tokens.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Prompt is one question for a model. Schema, when set, is a JSON Schema the
// answer must satisfy. TopLogProbs, when above zero, asks for that many
// alternatives per token.
type Prompt struct {
	System      string
	User        string
	MaxTokens   int
	Schema      []byte
	TopLogProbs int
}

// Completion is what a provider answered, and what it cost.
type Completion struct {
	Text    string
	Model   string
	Tokens  []Token
	Usage   Usage
	Latency time.Duration
}

// Provider turns a prompt into a completion. The caller does not know which
// vendor answered; Completion.Model records it.
type Provider interface {
	Name() string
	Complete(ctx context.Context, p Prompt) (Completion, error)
}

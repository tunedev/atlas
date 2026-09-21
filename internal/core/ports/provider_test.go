package ports_test

import (
	"context"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// stubProvider exists to prove the interface is implementable from outside the
// package with no adapter type in any signature.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Complete(_ context.Context, p ports.Prompt) (ports.Completion, error) {
	return ports.Completion{
		Text:    "answer to " + p.User,
		Model:   "stub-model",
		Latency: time.Millisecond,
		Usage:   ports.Usage{PromptTokens: 3, CompletionTokens: 4},
		Tokens: []ports.Token{{
			Text:    "yes",
			LogProb: -1.3,
			Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.4},
				{Text: "no", LogProb: -4.4},
			},
		}},
	}, nil
}

func TestAProviderCanBeImplementedOutsideTheCore(t *testing.T) {
	var p ports.Provider = stubProvider{}

	got, err := p.Complete(context.Background(), ports.Prompt{
		System:      "be terse",
		User:        "a question",
		MaxTokens:   8,
		TopLogProbs: 3,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got.Model == "" {
		t.Error("Completion does not record which model answered")
	}
	if got.Usage.CompletionTokens == 0 {
		t.Error("Completion does not carry usage")
	}
	if got.Latency == 0 {
		t.Error("Completion does not carry latency")
	}
	if len(got.Tokens) != 1 || len(got.Tokens[0].Alternatives) != 2 {
		t.Fatalf("Completion does not carry per-token alternatives: %+v", got.Tokens)
	}
	if got.Tokens[0].Alternatives[0].Text != "Yes" {
		t.Errorf("alternative text = %q", got.Tokens[0].Alternatives[0].Text)
	}
}

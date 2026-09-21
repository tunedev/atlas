package openaiprov_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// yesNoSchema constrains the answer to an object with a single "answer"
// field restricted to the enum ["yes", "no"].
const yesNoSchema = `{
 "type": "object",
 "properties": {"answer": {"type": "string", "enum": ["yes", "no"]}},
 "required": ["answer"]
}`

var yesNoClasses = map[string][]string{
	"yes": {"yes", "Yes", "YES", "true"},
	"no":  {"no", "No", "NO", "false"},
}

// TestMassPerClassAgainstALiveEngine is skipped unless ATLAS_LIVE_PROVIDER is
// set, so CI never depends on a running engine. The base URL and model come
// from ATLAS_LIVE_BASE_URL and ATLAS_LIVE_MODEL, defaulting to a local
// Ollama. It asks a schema-constrained yes/no question with an obvious
// answer and checks that app.MassPerClass reads the model's real confidence,
// not the chosen token's own probability.
func TestMassPerClassAgainstALiveEngine(t *testing.T) {
	if os.Getenv("ATLAS_LIVE_PROVIDER") == "" {
		t.Skip("ATLAS_LIVE_PROVIDER not set")
	}
	baseURL := os.Getenv("ATLAS_LIVE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	model := os.Getenv("ATLAS_LIVE_MODEL")
	if model == "" {
		model = "qwen2.5-coder:7b"
	}
	c := openaiprov.New(openaiprov.Config{
		Name: "live", BaseURL: baseURL, Model: model,
		Timeout: 3 * time.Minute, MaxBytes: 10 << 20,
	})

	got, err := c.Complete(context.Background(), ports.Prompt{
		User:        `Is the sky blue on a clear day? Answer as JSON: {"answer": "yes" or "no"}.`,
		MaxTokens:   20,
		Schema:      []byte(yesNoSchema),
		TopLogProbs: 5,
	})
	if err != nil {
		t.Fatalf("live complete: %v", err)
	}

	mass, err := app.MassPerClass(got, yesNoClasses)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	t.Logf("mass per class: %+v", mass)
	t.Logf("chosen answer token: %s", chosenTokenReport(got))

	if mass["yes"] <= 0.8 {
		t.Errorf("yes = %.4f, want above 0.8", mass["yes"])
	}
}

// chosenTokenReport describes the last token of the completion and its own
// probability, for comparison against the mass MassPerClass computed.
func chosenTokenReport(c ports.Completion) string {
	if len(c.Tokens) == 0 {
		return "no per-token data"
	}
	last := c.Tokens[len(c.Tokens)-1]
	return fmt.Sprintf("text=%q probability=%.4f", last.Text, math.Exp(last.LogProb))
}

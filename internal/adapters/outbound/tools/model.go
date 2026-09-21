package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Model calls a Provider to complete a prompt. Which vendor answers, and how,
// is the provider's business; the tool only knows the port.
//
// With expect=json the reply is parsed into fields a pack can select by path.
// Without it the reply is returned as text under "text". Either way the
// answering model is recorded under "model", so the platform can state which
// provider produced any given output.
type Model struct {
	provider ports.Provider
}

func NewModel(p ports.Provider) *Model {
	return &Model{provider: p}
}

func (m *Model) Name() string { return "model.complete" }

func (m *Model) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if with["user"] == "" {
		return nil, fmt.Errorf("model.complete: no user message")
	}

	c, err := m.provider.Complete(ctx, ports.Prompt{
		System: with["system"],
		User:   with["user"],
	})
	if err != nil {
		return nil, fmt.Errorf("model.complete: %w", err)
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("gen_ai.response.model", c.Model),
		attribute.Int("gen_ai.usage.input_tokens", c.Usage.PromptTokens),
		attribute.Int("gen_ai.usage.output_tokens", c.Usage.CompletionTokens),
		attribute.Int64("gen_ai.latency_ms", c.Latency.Milliseconds()),
	)

	if with["expect"] != "json" {
		return map[string]any{"text": c.Text, "model": c.Model}, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(unfence(c.Text)), &fields); err != nil {
		return nil, fmt.Errorf("model.complete: expected json, got %q: %w", c.Text, err)
	}
	fields["model"] = c.Model
	return fields, nil
}

// unfence strips a markdown code fence. Small local models wrap JSON in one
// routinely, and failing on formatting rather than on substance would be the
// wrong reason to fail.
func unfence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "```"))
}

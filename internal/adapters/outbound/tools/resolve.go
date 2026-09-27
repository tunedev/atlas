package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
)

// SpanResolve turns the span ids a model cited into citations carrying each
// span's verbatim text, so a quote is copied, never typed.
type SpanResolve struct{}

func NewSpanResolve() *SpanResolve { return &SpanResolve{} }

func (r *SpanResolve) Name() string { return "span.resolve" }

func (r *SpanResolve) Invoke(_ context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("span.resolve: fields: %w", err)
	}
	var spans []app.Span
	if err := json.Unmarshal([]byte(with["spans"]), &spans); err != nil {
		return nil, fmt.Errorf("span.resolve: spans: %w", err)
	}
	return map[string]any{"fields": app.ResolveCitations(tree, spans)}, nil
}

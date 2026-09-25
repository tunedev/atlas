package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestQuoteGroundAnnotatesAndCounts(t *testing.T) {
	out, err := tools.NewQuoteGround().Invoke(context.Background(), map[string]string{
		"fields": `{"sightings":[{"quote":"kestrel at dawn"},{"quote":"a puffin"}]}`,
		"source": "A kestrel at dawn over the dunes.",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := out.(map[string]any)
	if m["needs_review"] != 1 {
		t.Errorf("needs_review = %v, want 1", m["needs_review"])
	}
	first := m["fields"].(map[string]any)["sightings"].([]any)[0].(map[string]any)
	if first["status"] != "grounded" {
		t.Errorf("first = %v", first)
	}
}

func TestQuoteGroundRejectsMalformedFieldsAndEmptySource(t *testing.T) {
	for name, with := range map[string]map[string]string{
		"trailing comma": {"fields": `{"quote":"x",}`, "source": "x"},
		"no fields":      {"source": "x"},
		"no source":      {"fields": `{"quote":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tools.NewQuoteGround().Invoke(context.Background(), with)
			if err == nil || !strings.HasPrefix(err.Error(), "quote.ground: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

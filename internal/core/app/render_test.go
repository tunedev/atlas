package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
)

func TestRenderSubstitutesVars(t *testing.T) {
	s := domain.NewState(map[string]string{"host": "example.com"})
	got, err := app.Render("https://{{ .vars.host }}/x", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "https://example.com/x" {
		t.Errorf("got %q", got)
	}
}

func TestRenderReadsEarlierStepOutputByPath(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("first", map[string]any{"title": "Widget", "inner": map[string]any{"deep": "value"}})

	got, err := app.Render("{{ .steps.first.title }} / {{ .steps.first.inner.deep }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "Widget / value" {
		t.Errorf("got %q", got)
	}
}

func TestRenderFailsOnAMissingKeyRatherThanEmittingNoValue(t *testing.T) {
	// A silently empty prompt is far worse than a failed run: the model would
	// be asked to work from nothing and would answer anyway.
	if _, err := app.Render("{{ .steps.absent.title }}", domain.NewState(nil)); err == nil {
		t.Error("Render succeeded on a missing key")
	}
}

func TestRenderFailsOnAPresentKeyWithAJSONNullValue(t *testing.T) {
	// missingkey=error only fires when a key is absent. A key present with a
	// JSON null decodes to a nil interface, which the default template
	// formatter renders as the literal string "<no value>" rather than
	// erroring. That string reaching a prompt is exactly the silently wrong
	// result missingkey=error exists to prevent.
	s := domain.NewState(nil)
	s.Put("item", map[string]any{"title": nil})

	if _, err := app.Render("{{ .steps.item.title }}", s); err == nil {
		t.Error("Render succeeded on a present key with a null value")
	}
}

func TestRenderFormatsAnIntegralFloatWithoutScientificNotation(t *testing.T) {
	// Tool results decode JSON numbers as float64. text/template's default
	// %v formatting renders a large integral float64 in scientific notation,
	// which mangles an id, a timestamp, or any other whole number a pack
	// interpolates into a prompt.
	s := domain.NewState(nil)
	s.Put("item", map[string]any{"time": float64(1175714200)})

	got, err := app.Render("{{ .steps.item.time }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "1175714200" {
		t.Errorf("got %q, want plain integer", got)
	}
}

func TestRenderLeavesPlainTextAlone(t *testing.T) {
	got, err := app.Render("no templates here", domain.NewState(nil))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "no templates here" {
		t.Errorf("got %q", got)
	}
}

func TestSelectNarrowsByDottedPath(t *testing.T) {
	value := map[string]any{
		"items": []any{
			map[string]any{"name": "first"},
			map[string]any{"name": "second"},
		},
	}
	got, err := app.Select(value, "items.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["name"] != "first" {
		t.Errorf("got %#v", got)
	}
}

func TestSelectWithAnEmptyPathReturnsTheWholeValue(t *testing.T) {
	got, err := app.Select(map[string]any{"a": 1}, "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got == nil {
		t.Error("Select with an empty path dropped the value")
	}
}

func TestSelectFailsOnAMissingKey(t *testing.T) {
	if _, err := app.Select(map[string]any{"a": 1}, "b"); err == nil {
		t.Error("Select succeeded on a missing key")
	}
}

func TestSelectFailsOnAnOutOfRangeIndex(t *testing.T) {
	if _, err := app.Select(map[string]any{"items": []any{}}, "items.0"); err == nil {
		t.Error("Select succeeded on an out-of-range index")
	}
}

func TestRenderFailsOnMalformedTemplate(t *testing.T) {
	if _, err := app.Render("{{ .vars.host ", domain.NewState(nil)); err == nil {
		t.Error("Render succeeded on malformed template")
	}
}

func TestSelectReturnsErrorNotPanicOnScalar(t *testing.T) {
	if _, err := app.Select("a string value", "foo"); err == nil {
		t.Error("Select succeeded on scalar with non-empty path")
	}
}

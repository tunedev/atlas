package app_test

import (
	"encoding/json"
	"strings"
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

func TestRenderFailsOnANullSliceElementAndNamesItsPath(t *testing.T) {
	// A null cannot be dropped from a slice the way a null map value is
	// dropped: dropping it would shift every later index. So a null inside a
	// slice must reject the render outright, and the error must name the
	// offending element so a pack author can find it.
	s := domain.NewState(nil)
	s.Put("item", map[string]any{"kids": []any{nil, "b"}})

	_, err := app.Render("{{ index .steps.item.kids 0 }}", s)
	if err == nil {
		t.Fatal("Render succeeded on a slice containing a null element")
	}
	if !strings.Contains(err.Error(), "steps.item.kids[0]") {
		t.Errorf("error %q does not name the path steps.item.kids[0]", err.Error())
	}
}

func TestRenderFailsOnANullNestedInsideASliceInsideAMapInsideASlice(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("item", []any{map[string]any{"kids": []any{"a", nil}}})

	_, err := app.Render("{{ .steps.item }}", s)
	if err == nil {
		t.Fatal("Render succeeded on a deeply nested null slice element")
	}
	if !strings.Contains(err.Error(), "steps.item[0].kids[1]") {
		t.Errorf("error %q does not name the path steps.item[0].kids[1]", err.Error())
	}
}

func TestRenderStillDropsANullMapKeyLazilyRatherThanErroringEagerly(t *testing.T) {
	// A null-valued map key is dropped before the template runs, so a
	// template that never references that key still renders successfully.
	// Only a null inside a slice is rejected eagerly.
	s := domain.NewState(nil)
	s.Put("item", map[string]any{"title": "ok", "extra": nil})

	got, err := app.Render("{{ .steps.item.title }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestRenderLeavesASliceWithoutNullsUnaffected(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("item", map[string]any{"kids": []any{"a", "b"}})

	got, err := app.Render("{{ index .steps.item.kids 0 }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "a" {
		t.Errorf("got %q, want %q", got, "a")
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

func TestRenderJSONSerialisesAStepOutputForAnotherStepsStringField(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("extract", map[string]any{"fields": map[string]any{
		"name":  "Kestrel & <Merlin>",
		"count": float64(3),
		"gone":  nil,
		"tags":  []any{"coast"},
	}})

	got, err := app.Render("{{ json .steps.extract.fields }}", s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("rendered text is not json: %v\n%s", err, got)
	}
	if back["name"] != "Kestrel & <Merlin>" {
		t.Errorf("name = %v", back["name"])
	}
	if !strings.Contains(got, `"count": 3`) {
		t.Errorf("a whole number did not render as one:\n%s", got)
	}
	if !strings.Contains(got, `&`) || !strings.Contains(got, `<`) {
		t.Errorf("literal & or < was escaped; output is HTML-escaped when it should not be:\n%s", got)
	}
	if !strings.Contains(got, "Kestrel & <Merlin>") {
		t.Errorf("the data did not round-trip through JSON correctly:\n%s", got)
	}
	if _, present := back["gone"]; present {
		t.Errorf("a null-valued key survived sanitising: %v", back)
	}
	if !strings.Contains(got, "\n  \"") {
		t.Errorf("json is not indented for a human to edit:\n%s", got)
	}
}

func TestRenderJSONOfAMissingStepStillFails(t *testing.T) {
	if _, err := app.Render("{{ json .steps.absent }}", domain.NewState(nil)); err == nil {
		t.Error("json of a step that never ran rendered instead of failing")
	}
}

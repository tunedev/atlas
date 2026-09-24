package app_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

const sightingsSource = `Field log, spring
Kestrel   hovering over the
dune path at dawn.
Two OYSTERCATCHERS on the breakwater.`

func TestGroundReportsOnlyQuotesAbsentFromTheSource(t *testing.T) {
	extracted := json.RawMessage(`{
		"sightings": [
			{"species": "kestrel", "quote": "kestrel hovering over the dune path",
			 "notes": [{"text": "at dawn", "quote": "at dawn"}, {"text": "eating", "quote": "eating a vole"}]},
			{"species": "oystercatcher", "quote": "two oystercatchers on the breakwater"},
			{"species": "puffin", "quote": "a puffin on the cliff"}
		]
	}`)
	got, err := app.Ground(extracted, sightingsSource)
	if err != nil {
		t.Fatalf("ground: %v", err)
	}
	want := []string{"/sightings/0/notes/1/quote", "/sightings/2/quote"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("ungrounded = %v, want %v", got, want)
	}
}

func TestGroundTreatsAnEmptyOrNonStringQuoteAsUngrounded(t *testing.T) {
	extracted := json.RawMessage(`{"a": {"quote": ""}, "b": {"quote": "   "}, "c": {"quote": 7}, "d": {"quote": null}}`)
	got, err := app.Ground(extracted, sightingsSource)
	if err != nil {
		t.Fatalf("ground: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("ungrounded = %v, want all four; an empty string is a substring of everything", got)
	}
}

func TestGroundSkipsObjectsWithNoQuote(t *testing.T) {
	got, err := app.Ground(json.RawMessage(`{"species": "wren", "list": [1, "two", {"x": 1}]}`), sightingsSource)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, err %v; objects without a quote key are not claims", got, err)
	}
}

func TestGroundEscapesPointerSegments(t *testing.T) {
	got, err := app.Ground(json.RawMessage(`{"a/b": {"c~d": {"quote": "absent"}}}`), sightingsSource)
	if err != nil {
		t.Fatalf("ground: %v", err)
	}
	if !slices.Equal(got, []string{"/a~1b/c~0d/quote"}) {
		t.Errorf("pointer = %v", got)
	}
}

func TestGroundRejectsInvalidJSON(t *testing.T) {
	if _, err := app.Ground(json.RawMessage(`{"quote": `), sightingsSource); err == nil {
		t.Fatal("invalid json was grounded")
	}
}

func TestAnnotateMarksEveryQuoteBearingObjectAndCountsTheFlags(t *testing.T) {
	extracted := json.RawMessage(`{"sightings": [
		{"species": "kestrel", "quote": "Kestrel hovering", "status": "grounded"},
		{"species": "puffin", "quote": "a puffin on the cliff", "status": "grounded"}
	]}`)
	tree, n, err := app.Annotate(extracted, sightingsSource)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if n != 1 {
		t.Errorf("needs_review count = %d, want 1", n)
	}
	items := tree.(map[string]any)["sightings"].([]any)
	if items[0].(map[string]any)["status"] != "grounded" {
		t.Errorf("real sighting = %v", items[0])
	}
	if items[1].(map[string]any)["status"] != "needs_review" {
		t.Errorf("invented sighting kept a hand-set grounded status: %v", items[1])
	}
	if items[1].(map[string]any)["species"] != "puffin" {
		t.Errorf("a flagged item was altered or dropped: %v", items[1])
	}
}

func TestAnnotateFlagsAStatusWithNoQuoteBesideIt(t *testing.T) {
	extracted := json.RawMessage(`{"sightings": [{"species": "puffin", "status": "grounded"}], "log": {"weather": "clear"}}`)
	tree, n, err := app.Annotate(extracted, sightingsSource)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if n != 1 {
		t.Errorf("needs_review count = %d, want 1", n)
	}
	items := tree.(map[string]any)["sightings"].([]any)
	if items[0].(map[string]any)["status"] != "needs_review" {
		t.Errorf("a status with no quote beside it was left alone: %v", items[0])
	}
	log := tree.(map[string]any)["log"].(map[string]any)
	if _, present := log["status"]; present {
		t.Errorf("an object with neither key was given a status: %v", log)
	}
}

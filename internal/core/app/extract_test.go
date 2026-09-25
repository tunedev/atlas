package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

const sightingsSchema = `{"type":"object","required":["sightings"],"additionalProperties":false,
"properties":{"sightings":{"type":"array","items":{"type":"object","required":["species","quote"],
"additionalProperties":false,"properties":{"species":{"type":"string"},"quote":{"type":"string"}}}}}}`

func extractConfig() app.ExtractorConfig { return app.ExtractorConfig{Temperature: 0, MaxTokens: 512} }

func TestExtractSendsTheCallersSchemaAndPinnedSampling(t *testing.T) {
	p := &recordingProvider{completion: ports.Completion{Text: `{"sightings":[]}`}}
	if _, err := app.NewExtractor(p, extractConfig()).Extract(context.Background(), sightingsSource, []byte(sightingsSchema)); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if string(p.last.Schema) != sightingsSchema {
		t.Errorf("schema on the wire = %s", p.last.Schema)
	}
	if p.last.Temperature == nil || *p.last.Temperature != 0 {
		t.Errorf("temperature = %v, want pinned 0", p.last.Temperature)
	}
	if p.last.MaxTokens != 512 {
		t.Errorf("max tokens = %d", p.last.MaxTokens)
	}
	if !strings.Contains(p.last.User, "Kestrel") {
		t.Errorf("the source text did not reach the model: %q", p.last.User)
	}
	if !strings.Contains(strings.ToLower(p.last.System), "verbatim") {
		t.Errorf("the system message does not ask for verbatim quotes: %q", p.last.System)
	}
}

func TestExtractRefusesANonJSONReplyRatherThanGuessing(t *testing.T) {
	p := &recordingProvider{completion: ports.Completion{Text: "Here are the sightings: kestrel"}}
	if _, err := app.NewExtractor(p, extractConfig()).Extract(context.Background(), sightingsSource, []byte(sightingsSchema)); err == nil {
		t.Fatal("a prose reply was accepted as an extraction")
	}
}

func TestExtractRefusesEmptyTextAndAnInvalidSchemaWithoutCallingTheModel(t *testing.T) {
	p := &recordingProvider{completion: ports.Completion{Text: `{}`}}
	e := app.NewExtractor(p, extractConfig())
	if _, err := e.Extract(context.Background(), "  \n", []byte(sightingsSchema)); err == nil {
		t.Error("empty text was sent for extraction")
	}
	if _, err := e.Extract(context.Background(), sightingsSource, []byte(`{"type":`)); err == nil {
		t.Error("an invalid schema was sent")
	}
	if p.calls != 0 {
		t.Errorf("provider called %d times for input that could never succeed", p.calls)
	}
}

func TestExtractWrapsAProviderFailure(t *testing.T) {
	_, err := app.NewExtractor(&failingProvider{}, extractConfig()).Extract(context.Background(), sightingsSource, []byte(sightingsSchema))
	if err == nil || !strings.HasPrefix(err.Error(), "extract: ") {
		t.Errorf("err = %v", err)
	}
}

// TestAnInventedValueIsFlaggedAndARealOneIsNot is the epic's central
// regression test: a canned completion with one sighting the source states
// and one it does not, through Extract then Annotate, with no model.
func TestAnInventedValueIsFlaggedAndARealOneIsNot(t *testing.T) {
	p := &recordingProvider{completion: ports.Completion{Text: `{"sightings":[
		{"species":"kestrel","quote":"Kestrel hovering over the dune path"},
		{"species":"puffin","quote":"a puffin nesting on the cliff"}]}`}}

	extracted, err := app.NewExtractor(p, extractConfig()).Extract(context.Background(), sightingsSource, []byte(sightingsSchema))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	tree, flagged, err := app.Annotate(extracted, sightingsSource)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	if flagged != 1 {
		t.Errorf("flagged = %d, want 1", flagged)
	}
	out, _ := json.Marshal(tree)
	items := tree.(map[string]any)["sightings"].([]any)
	if items[0].(map[string]any)["status"] != "grounded" {
		t.Errorf("the real sighting was flagged: %s", out)
	}
	if items[1].(map[string]any)["status"] != "needs_review" {
		t.Errorf("the invented sighting was not flagged: %s", out)
	}
	if len(items) != 2 {
		t.Errorf("a flagged value was dropped rather than kept for review: %s", out)
	}
}

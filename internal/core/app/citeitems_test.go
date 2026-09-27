package app_test

import (
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestCiteItemsPairsEachGroundedItemWithItsCitedSpans(t *testing.T) {
	items := decode(t, `[
	 {"quote":"keeps a ship log","status":"grounded"},
	 {"quote":"has sailed a tall ship","status":"needs_review"},
	 {"quote":"holds a harbour pilot licence","status":"grounded"}]`).([]any)
	cited := decode(t, `[
	 {"item":0,"spans":[0,1]},
	 {"item":0,"spans":[1,2]},
	 {"item":1,"spans":[9]},
	 {"item":5,"spans":[3]},
	 {"item":-1,"spans":[4]},
	 {"item":0.5,"spans":[5]}]`).([]any)

	got, dropped := app.CiteItems(items, cited)
	want := []any{
		map[string]any{"text": "keeps a ship log", "spans": []any{float64(0), float64(1), float64(2)}},
		map[string]any{"text": "holds a harbour pilot licence", "spans": []any{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %s\nwant %s", encode(t, got), encode(t, want))
	}
	if dropped != 1 {
		t.Errorf("dropped = %d; want 1 (the needs_review item)", dropped)
	}
}

func TestCiteItemsEmitsEveryGroundedItemEvenUncited(t *testing.T) {
	items := decode(t, `[{"quote":"keeps a ship log","status":"grounded"}]`).([]any)
	got, dropped := app.CiteItems(items, []any{})
	want := []any{map[string]any{"text": "keeps a ship log", "spans": []any{}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %s; want %s", encode(t, got), encode(t, want))
	}
	if dropped != 0 {
		t.Errorf("dropped = %d; want 0", dropped)
	}
}

func TestCiteItemsDropsEveryUngroundedItem(t *testing.T) {
	items := decode(t, `[
	 {"quote":"a","status":"needs_review"},
	 {"quote":"b","status":"needs_review"}]`).([]any)
	cited := decode(t, `[{"item":0,"spans":[0]}]`).([]any)
	got, dropped := app.CiteItems(items, cited)
	if len(got) != 0 {
		t.Errorf("got %v; want nothing emitted", got)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d; want 2", dropped)
	}
}

func TestGatherItemsGivesEveryGroundedItemItsSentences(t *testing.T) {
	items := decode(t, `[
	 {"quote":"Can you trim a wick?","status":"grounded"},
	 {"quote":"Can you sail?","status":"needs_review"},
	 {"quote":"Can you read a chart?","status":"grounded"}]`).([]any)
	answered := decode(t, `[
	 {"item":0,"sentences":[{"text":"I trimmed wicks nightly.","spans":[0]}]},
	 {"item":0,"sentences":[{"text":"I kept spares.","spans":[1]}]},
	 {"item":1,"sentences":[{"text":"I sailed.","spans":[2]}]},
	 {"item":7,"sentences":[{"text":"Out of range.","spans":[3]}]},
	 {"item":0.5,"sentences":[{"text":"Fractional.","spans":[4]}]}]`).([]any)

	got, dropped := app.GatherItems(items, answered)
	want := decode(t, `[
	 {"item":"Can you trim a wick?","sentences":[
	   {"text":"I trimmed wicks nightly.","spans":[0]},
	   {"text":"I kept spares.","spans":[1]}]},
	 {"item":"Can you read a chart?","sentences":[{"text":"","spans":[]}]}]`)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %s\nwant %s", encode(t, got), encode(t, want))
	}
	if dropped != 1 {
		t.Errorf("dropped = %d; want 1", dropped)
	}
}

func TestAnUnansweredItemSettlesAsAGap(t *testing.T) {
	items := decode(t, `[{"quote":"Can you read a chart?","status":"grounded"}]`).([]any)
	got, _ := app.GatherItems(items, []any{})
	tree := app.ResolveCitations(map[string]any{"answers": got}, keeperSpans)
	if s := app.Settle(tree); s.Gaps != 1 {
		t.Errorf("gaps = %d; want 1: an unanswered item is a gap\n%s", s.Gaps, encode(t, s.Tree))
	}
}

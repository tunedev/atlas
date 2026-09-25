package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestDedupeCountsTheSameItemOnceAcrossSpellings(t *testing.T) {
	items := []any{
		map[string]any{"venue": "Harbour Library", "name": "Tide Talk", "from": "listing"},
		map[string]any{"venue": "harbour library", "name": "Tide talk!", "from": "aggregator"},
		map[string]any{"venue": "Harbour  library", "name": "tide-talk", "from": "own page"},
		map[string]any{"venue": "Harbour library", "name": "Knot workshop"},
	}
	kept, merged := app.Dedupe(items, []string{"venue", "name"})
	if len(kept) != 2 || merged != 2 {
		t.Fatalf("kept = %v merged = %d", kept, merged)
	}
	if kept[0].(map[string]any)["from"] != "listing" {
		t.Errorf("first kept = %v; the first occurrence wins", kept[0])
	}
}

func TestDedupeNeverMergesAnItemMissingAKeyField(t *testing.T) {
	items := []any{
		map[string]any{"name": "Tide talk"},
		map[string]any{"name": "Tide talk"},
		"not an object",
	}
	kept, merged := app.Dedupe(items, []string{"venue", "name"})
	if len(kept) != 3 || merged != 0 {
		t.Errorf("kept = %v merged = %d; an item with no full key cannot be proven a duplicate", kept, merged)
	}
}

func TestDedupeNeverMergesItemsWhosePunctuationOnlyKeyFieldNormalisesToEmpty(t *testing.T) {
	items := []any{
		map[string]any{"venue": "Harbour library", "name": "--"},
		map[string]any{"venue": "Harbour library", "name": "???"},
	}
	kept, merged := app.Dedupe(items, []string{"venue", "name"})
	if len(kept) != 2 || merged != 0 {
		t.Errorf("kept = %v merged = %d; a key field with no letter or digit normalises to empty and cannot be proven a duplicate", kept, merged)
	}
}

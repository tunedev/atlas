package tools_test

import (
	"context"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestItemsCiteTool(t *testing.T) {
	out, err := tools.NewItemsCite().Invoke(context.Background(), map[string]string{
		"items": `[{"quote":"keeps a ship log","status":"grounded"},
		           {"quote":"has sailed a tall ship","status":"needs_review"}]`,
		"cited": `[{"item":0,"spans":[0,1]}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	items := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v; want the one grounded item", items)
	}
	first := items[0].(map[string]any)
	if first["text"] != "keeps a ship log" {
		t.Errorf("text = %v", first["text"])
	}
	if res["dropped"] != 1 {
		t.Errorf("dropped = %v; want 1", res["dropped"])
	}
}

func TestItemsCiteRejectsBadInput(t *testing.T) {
	for name, with := range map[string]map[string]string{
		"bad items": {"items": "{", "cited": "[]"},
		"bad cited": {"items": "[]", "cited": "{"},
	} {
		if _, err := tools.NewItemsCite().Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

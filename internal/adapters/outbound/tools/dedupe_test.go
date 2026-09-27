package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestDedupeMergesAcrossLists(t *testing.T) {
	out, err := tools.NewDedupe().Invoke(context.Background(), map[string]string{
		"lists": `[[{"venue":"Harbour library","name":"Tide talk"}],
		           [{"venue":"harbour library","name":"Tide Talk"},{"venue":"Harbour library","name":"Knot workshop"}]]`,
		"key": "venue, name",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := out.(map[string]any)
	meta := m["_meta"].(map[string]any)
	if len(m["items"].([]any)) != 2 || meta["in"] != 3 || meta["out"] != 2 || meta["merged"] != 1 {
		t.Errorf("out = %v", m)
	}
}

func TestDedupeRejectsWhatItCannotRead(t *testing.T) {
	for name, with := range map[string]map[string]string{
		"no key":         {"lists": `[[]]`},
		"not json":       {"lists": `[[`, "key": "a"},
		"not a list":     {"lists": `{"a":1}`, "key": "a"},
		"inner not list": {"lists": `[{"a":1}]`, "key": "a"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tools.NewDedupe().Invoke(context.Background(), with)
			if err == nil || !strings.HasPrefix(err.Error(), "items.dedupe: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

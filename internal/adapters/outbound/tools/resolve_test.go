package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestSpanResolveTool(t *testing.T) {
	out, err := tools.NewSpanResolve().Invoke(context.Background(), map[string]string{
		"fields": `{"claims":[{"text":"keeps records","spans":[0]}]}`,
		"spans":  `[{"id":0,"source":"log.txt","text":"Logged every passing ship"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out.(map[string]any)["fields"])
	if string(b) != `{"claims":[{"citations":[{"id":0,"quote":"Logged every passing ship"}],"text":"keeps records"}]}` {
		t.Errorf("got %s", b)
	}
	for _, bad := range []map[string]string{{"fields": "{", "spans": "[]"}, {"fields": "{}", "spans": "{"}} {
		if _, err := tools.NewSpanResolve().Invoke(context.Background(), bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

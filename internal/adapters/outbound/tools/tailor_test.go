package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

// The canned extraction a model might return: one real citation, one
// irrelevant one, one out-of-range id, and a statement with nothing behind
// it — the three ways tailoring invents.
const cannedExtraction = `{
 "requirements":[
  {"text":"keeps a ship log","spans":[0]},
  {"text":"holds a harbour pilot licence","spans":[1]},
  {"text":"has sailed a tall ship","spans":[42]}],
 "bullets":[{"spans":[0]},{"spans":[99]}],
 "letter":[
  {"text":"I kept a careful log of every passing ship.","spans":[0]},
  {"text":"I captained a tall ship for ten years.","spans":[]}],
 "answers":[{"question":"Can you pilot a harbour?","sentences":[{"text":"Yes, I am licensed.","spans":[1]}]}]
}`

func TestNothingUnsupportedSurvivesTailoring(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "log.txt")
	must(t, os.WriteFile(src, []byte("Logged every passing ship by name and hour\nRefitted the lamp lens in winter\n"), 0o600))

	step := func(out any, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	spans := step(tools.NewTextSpans(1<<20).Invoke(ctx, map[string]string{"paths": src, "min_chars": "10"}))
	resolved := step(tools.NewSpanResolve().Invoke(ctx, map[string]string{"fields": cannedExtraction, "spans": js(spans["spans"])}))
	grounded := step(tools.NewQuoteGround().Invoke(ctx, map[string]string{"fields": js(resolved["fields"]), "source": spans["source"].(string)}))
	docs, index := store(t)
	judged := step(tools.NewCitationsJudge(&relevanceJudge{yes: []string{"ship"}}, docs, index).Invoke(ctx,
		map[string]string{"fields": js(grounded["fields"]), "threshold": "0.6", "subject_id": "keeper"}))
	settled := step(tools.NewClaimsSettle().Invoke(ctx, map[string]string{"fields": js(judged["fields"])}))

	kept := js(settled["kept"])
	for _, invented := range []string{"pilot licence", "tall ship", "captained", "licensed", "Refitted"} {
		if strings.Contains(kept, invented) {
			t.Errorf("kept output states %q, which nothing supports:\n%s", invented, kept)
		}
	}
	if !strings.Contains(kept, "Logged every passing ship by name and hour") ||
		!strings.Contains(kept, "I kept a careful log of every passing ship.") {
		t.Errorf("the supported claims were dropped:\n%s", kept)
	}
	if settled["gaps"] != 5 {
		t.Errorf("gaps = %v; want 5 (two requirements, one bullet, one letter sentence, one answer sentence)\n%s", settled["gaps"], js(settled["fields"]))
	}
}

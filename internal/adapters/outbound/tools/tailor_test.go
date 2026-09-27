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

// cannedNotice names four requirements verbatim, the way a real record of
// requirements would; cannedAsked is the model's copy of them, and
// cannedCited is the model's citation of candidate spans against three of
// the four — the fourth is never cited, the way a real model sometimes
// drops one silently.
const cannedNotice = `Duties: keeps a ship log daily. Must hold a harbour pilot licence.
Must have sailed a tall ship. Must hold a first aid certificate.`

const cannedAsked = `{"requirements":[
 {"quote":"keeps a ship log"},
 {"quote":"hold a harbour pilot licence"},
 {"quote":"have sailed a tall ship"},
 {"quote":"hold a first aid certificate"}]}`

const cannedCited = `[
 {"item":0,"spans":[0]},
 {"item":1,"spans":[1]},
 {"item":2,"spans":[42]}]`

// cannedExtraction is the canned extraction for bullets, letter and answers:
// one real citation, one irrelevant one, one out-of-range id, an
// empty-text statement, and a statement with nothing behind it — the ways
// tailoring invents beyond the requirements items.cite guards.
const cannedExtraction = `{
 "bullets":[{"spans":[0]},{"spans":[99]}],
 "letter":[
  {"text":"I kept a careful log of every passing ship.","spans":[0]},
  {"text":"I captained a tall ship for ten years.","spans":[]},
  {"text":"","spans":[0]}],
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

	// The requirements pipeline: what is asked for comes from the source
	// notice alone, never from what the model claims it can support.
	asked := step(tools.NewQuoteGround().Invoke(ctx, map[string]string{"fields": cannedAsked, "source": cannedNotice}))
	askedItems := asked["fields"].(map[string]any)["requirements"]
	cited := step(tools.NewItemsCite().Invoke(ctx, map[string]string{"items": js(askedItems), "cited": cannedCited}))

	var fields map[string]any
	must(t, json.Unmarshal([]byte(cannedExtraction), &fields))
	fields["requirements"] = cited["items"]

	resolved := step(tools.NewSpanResolve().Invoke(ctx, map[string]string{"fields": js(fields), "spans": js(spans["spans"])}))
	grounded := step(tools.NewQuoteGround().Invoke(ctx, map[string]string{"fields": js(resolved["fields"]), "source": spans["source"].(string)}))
	docs, index := store(t)
	judged := step(tools.NewCitationsJudge(&relevanceJudge{yes: []string{"ship"}}, docs, index).Invoke(ctx,
		map[string]string{"fields": js(grounded["fields"]), "threshold": "0.6", "subject_id": "keeper"}))
	settled := step(tools.NewClaimsSettle().Invoke(ctx, map[string]string{"fields": js(judged["fields"])}))

	kept := js(settled["kept"])
	for _, invented := range []string{"pilot licence", "tall ship", "captained", "licensed", "Refitted", "first aid certificate"} {
		if strings.Contains(kept, invented) {
			t.Errorf("kept output states %q, which nothing supports:\n%s", invented, kept)
		}
	}
	for _, supported := range []string{"keeps a ship log", "Logged every passing ship by name and hour", "I kept a careful log of every passing ship."} {
		if !strings.Contains(kept, supported) {
			t.Errorf("the supported claim %q was dropped:\n%s", supported, kept)
		}
	}
	// requirements: 1 relevant, 1 irrelevant, 1 out-of-range, 1 never cited.
	// bullets: 1 kept, 1 out-of-range. letter: 1 kept, 1 uncited, 1 empty
	// text. answers: 1 irrelevant.
	if settled["gaps"] != 7 {
		t.Errorf("gaps = %v; want 7\n%s", settled["gaps"], js(settled["fields"]))
	}
}

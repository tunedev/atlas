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

// cannedQuestions are the questions a person asked, one per line;
// cannedAnswered answers only the first, the way a real model sometimes
// skips one.
const cannedQuestions = "Can you pilot a harbour?\n\nCan you trim a wick?\n"

const cannedAnswered = `[{"item":0,"sentences":[{"text":"Yes, I am licensed.","spans":[1]}]}]`

// cannedExtraction is the canned extraction for bullets and the letter: one
// real citation, one irrelevant one, one out-of-range id, an empty-text
// statement, a statement with nothing behind it, and one claiming gap:false
// with no spans at all — the ways tailoring invents beyond the requirements
// items.cite guards.
const cannedExtraction = `{
 "bullets":[{"spans":[0]},{"spans":[99]}],
 "letter":[
  {"text":"I kept a careful log of every passing ship.","spans":[0]},
  {"text":"I captained a tall ship for ten years.","spans":[]},
  {"text":"I was the harbour master.","gap":false},
  {"text":"","spans":[0]}]
}`

// tailorCanned runs the canned extraction through every tailoring tool, as
// the pack does, and returns claims.settle's output.
func tailorCanned(t *testing.T) map[string]any {
	t.Helper()
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

	spans := step(tools.NewTextSpans(1<<20).Invoke(ctx, map[string]string{"paths": src, "min_chars": "10"}))

	// What is asked for comes from the source notice and the typed
	// questions alone, never from what the model claims it can support.
	asked := step(tools.NewQuoteGround().Invoke(ctx, map[string]string{"fields": cannedAsked, "source": cannedNotice}))
	askedItems := asked["fields"].(map[string]any)["requirements"]
	cited := step(tools.NewItemsCite().Invoke(ctx, map[string]string{"items": js(askedItems), "cited": cannedCited}))
	questions := step(tools.NewTextLines().Invoke(ctx, map[string]string{"text": cannedQuestions}))
	answers := step(tools.NewItemsGather().Invoke(ctx, map[string]string{"items": js(questions["items"]), "answered": cannedAnswered}))

	var extracted map[string]any
	must(t, json.Unmarshal([]byte(cannedExtraction), &extracted))
	fields := map[string]any{
		"requirements": cited["items"],
		"bullets":      extracted["bullets"],
		"letter":       map[string]any{"sentences": extracted["letter"], "answers": answers["items"]},
	}

	resolved := step(tools.NewSpanResolve().Invoke(ctx, map[string]string{"fields": js(fields), "spans": js(spans["spans"])}))
	grounded := step(tools.NewQuoteGround().Invoke(ctx, map[string]string{"fields": js(resolved["fields"]), "source": spans["source"].(string)}))
	docs, index := store(t)
	judged := step(tools.NewCitationsJudge(&relevanceJudge{yes: []string{"ship"}}, docs, index).Invoke(ctx,
		map[string]string{"fields": js(grounded["fields"]), "threshold": "0.6", "subject_id": "keeper"}))
	return step(tools.NewClaimsSettle().Invoke(ctx, map[string]string{"fields": js(judged["fields"])}))
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestNothingUnsupportedSurvivesTailoring(t *testing.T) {
	settled := tailorCanned(t)

	kept := js(settled["kept"])
	for _, invented := range []string{"pilot licence", "tall ship", "captained", "licensed", "harbour master", "Refitted", "first aid certificate", "trim a wick"} {
		if strings.Contains(kept, invented) {
			t.Errorf("kept output states %q, which nothing supports:\n%s", invented, kept)
		}
	}
	for _, supported := range []string{"keeps a ship log", "Logged every passing ship by name and hour", "I kept a careful log of every passing ship."} {
		if !strings.Contains(kept, supported) {
			t.Errorf("the supported claim %q was dropped:\n%s", supported, kept)
		}
	}
	// Every unsupported statement with text reaches the review data.
	gapsText := js(settled["gaps_text"])
	for _, gap := range []string{"hold a harbour pilot licence", "have sailed a tall ship", "hold a first aid certificate",
		"I captained a tall ship for ten years.", "I was the harbour master.", "Yes, I am licensed."} {
		if !strings.Contains(gapsText, gap) {
			t.Errorf("gap %q is missing from the review data:\n%s", gap, gapsText)
		}
	}
	// requirements: 1 relevant, 1 irrelevant, 1 out-of-range, 1 never cited.
	// bullets: 1 kept, 1 out-of-range. letter: 1 kept, 1 uncited, 1 with no
	// spans, 1 empty text. answers: 1 irrelevant, 1 unanswered.
	if settled["gaps"] != 9 {
		t.Errorf("gaps = %v; want 9\n%s", settled["gaps"], js(settled["fields"]))
	}
}

// TestASendableRenderFailsWhenAGapIsPrinted feeds the settled document to
// render.run with the pack's absent input: a template that prints only kept
// sentences renders, and one that also prints a gap fails, publishing
// nothing.
func TestASendableRenderFailsWhenAGapIsPrinted(t *testing.T) {
	r := renderer(t)
	settled := tailorCanned(t)
	data := js(settled["fields"])
	absent := js(settled["gaps_text"].(map[string][]string)["letter"])
	expect := js(settled["kept"].(map[string][]string)["letter"])

	for name, tc := range map[string]struct {
		filter  string
		wantErr bool
	}{
		"kept only":   {".filter(s => not s.gap)", false},
		"prints gaps": {"", true},
	} {
		tmpl := writeTemplate(t, "#set text(hyphenate: false)\n"+
			"#for s in data.letter.sentences"+tc.filter+" [ #s.text ]\n"+
			"#for a in data.letter.answers [ #for s in a.sentences"+tc.filter+" [ #s.text ] ]\n")
		out := filepath.Join(t.TempDir(), "doc.pdf")
		_, err := r.Invoke(context.Background(), map[string]string{
			"template": tmpl, "data": data, "output": out, "expect": expect, "absent": absent,
		})
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v; want error %v", name, err, tc.wantErr)
		}
		if _, statErr := os.Stat(out); tc.wantErr && !os.IsNotExist(statErr) {
			t.Errorf("%s: output published although a gap was printed", name)
		}
	}
}

// TestInstructionsRenderFromSettledOutput renders the shipped instructions
// template from claims.settle's real output, bound the way the pack binds
// it: every gap reaches the check list, and a question with no kept answer
// sentence is not offered for pasting.
func TestInstructionsRenderFromSettledOutput(t *testing.T) {
	r := renderer(t)
	settled := tailorCanned(t)
	letter := settled["fields"].(map[string]any)["letter"].(map[string]any)
	data := js(map[string]any{
		"url":        "https://example.invalid/lamp/7",
		"subject_id": "lamp:7",
		"files":      []string{"a.pdf"},
		"answers":    letter["answers"],
		"gaps":       settled["gaps_all"],
	})
	expect := append([]string{"https://example.invalid/lamp/7"}, settled["gaps_all"].([]string)...)
	_, err := r.Invoke(context.Background(), map[string]string{
		"template": filepath.Join("..", "..", "..", "..", "packs", "tailor", "instructions.typ"),
		"data":     data,
		"output":   filepath.Join(t.TempDir(), "instructions.pdf"),
		"expect":   js(expect),
		"absent":   js([]string{"Can you pilot a harbour?", "Can you trim a wick?"}),
	})
	if err != nil {
		t.Fatal(err)
	}
}

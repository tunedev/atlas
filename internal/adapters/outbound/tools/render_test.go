package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/tabula"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
	"github.com/tunedev/atlas/internal/core/app"
)

func renderer(t *testing.T) *tools.Render {
	t.Helper()
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst is not on PATH")
	}
	c, err := typstconv.New(typstconv.Config{Bin: "typst", Timeout: time.Minute, MaxBytes: 1 << 24})
	if err != nil {
		t.Fatal(err)
	}
	return tools.NewRender(c, 1<<24)
}

func writeTemplate(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.typ")
	must(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// extractText extracts path's text with tabula and applies the same
// hyphen-wrap join render.go uses before grounding, so a test can check
// what actually landed on the page.
func extractText(t *testing.T, path string) string {
	t.Helper()
	text, _, err := tabula.Open(path).Text()
	must(t, err)
	return strings.ReplaceAll(text, "-\n", "-")
}

const hostile = `Cut costs by 30% #set page(fill: red) *bold* $x$ <b>tag</b> \ "quoted"`

// longWithHyphenWrap is long enough, at this template's default text size
// and page margins, that typst wraps the line inside "north-lighthouse-
// keeper" itself: measured directly against typst 0.15.1 and tabula, the
// break lands right after the hyphen with no trailing space. It keeps the
// "-\n" join in verifyText guarded by a real wrap, not a word-boundary one.
const longWithHyphenWrap = "word word word word word word word word word word word word word " +
	"north-lighthouse-keeper more filler words here to continue onward and onward"

func TestRenderKeepsDataAsTextAndVerifiesIt(t *testing.T) {
	tmpl := writeTemplate(t, "#set text(hyphenate: false)\n#for b in data.items [ - #b ]\n")
	out := filepath.Join(t.TempDir(), "nested", "doc.pdf")
	items := `["` + longWithHyphenWrap + `", "` + strings.ReplaceAll(strings.ReplaceAll(hostile, `\`, `\\`), `"`, `\"`) + `"]`
	res, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":` + items + `}`,
		"output":   out,
		"expect":   items,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["verified"] != 2 {
		t.Errorf("result %v", res)
	}
}

func TestRenderFailsWhenAnExpectedStringIsMissing(t *testing.T) {
	tmpl := writeTemplate(t, "#for b in data.items [ - #b ]\n")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the light"]}`,
		"output":   filepath.Join(t.TempDir(), "doc.pdf"),
		"expect":   `["Kept the light", "Sailed the ship"]`,
	})
	if err == nil || !strings.Contains(err.Error(), "Sailed the ship") || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("err = %v", err)
	}
}

func TestRenderFailsWhenOutputPathIsMissing(t *testing.T) {
	tmpl := writeTemplate(t, "#for b in data.items [ - #b ]\n")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the light"]}`,
		"output":   "",
	})
	if err == nil || !strings.Contains(err.Error(), "no output path") {
		t.Errorf("err = %v", err)
	}
}

func TestRenderDoesNotPublishWhenVerificationFails(t *testing.T) {
	tmpl := writeTemplate(t, "#for b in data.items [ - #b ]\n")
	out := filepath.Join(t.TempDir(), "doc.pdf")
	must(t, os.WriteFile(out, []byte("old bytes"), 0o600))

	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the light"]}`,
		"output":   out,
		"expect":   `["Sailed the ship"]`,
	})
	if err == nil {
		t.Fatal("want an error when an expected string is missing")
	}
	got, readErr := os.ReadFile(out)
	must(t, readErr)
	if string(got) != "old bytes" {
		t.Errorf("output = %q, want the pre-existing file left untouched", got)
	}
}

func TestBeforeScopesExpectToTheTextPrecedingTheEarliestMarker(t *testing.T) {
	tmpl := writeTemplate(t, "#set text(hyphenate: false)\nKept the beacon lit.\n= Removed\nSailed away at dawn.\n")
	out := filepath.Join(t.TempDir(), "doc.pdf")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{}`,
		"output":   out,
		"expect":   `["Kept the beacon lit."]`,
		"before":   "Removed",
	})
	if err != nil {
		t.Errorf("a string before the marker should verify: %v", err)
	}
}

func TestBeforeMarkerHidesTextThatComesAfterIt(t *testing.T) {
	tmpl := writeTemplate(t, "#set text(hyphenate: false)\nKept the beacon lit.\n= Removed\nSailed away at dawn.\n")
	out := filepath.Join(t.TempDir(), "doc.pdf")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{}`,
		"output":   out,
		"expect":   `["Sailed away at dawn."]`,
		"before":   "Removed",
	})
	if err == nil || !strings.Contains(err.Error(), "Sailed away at dawn") {
		t.Errorf("err = %v; a string only after the marker must fail verification", err)
	}
}

// packFixture is a pack's own description of one template render to check:
// which template, what data to bind, which strings must be grounded (in
// the text before the earliest "before" marker, when given), and which
// strings must not appear anywhere in the rendered text. The Go harness
// never names a template file or a pack's own wording; it only reads this
// shape.
type packFixture struct {
	Template string          `json:"template"`
	Data     json.RawMessage `json:"data"`
	Expect   []string        `json:"expect"`
	Before   []string        `json:"before"`
	Absent   []string        `json:"absent"`
}

// TestPackTemplateFixturesRender renders every pack's testdata fixture
// through render.run and checks its expect and absent strings. It knows
// nothing about any pack's templates or vocabulary; a pack that adds a
// fixture is covered without a Go change.
func TestPackTemplateFixturesRender(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "packs", "*", "testdata", "*.json"))
	must(t, err)
	if len(paths) == 0 {
		t.Fatal("no pack template fixtures found")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			must(t, err)
			var fx packFixture
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			must(t, dec.Decode(&fx))

			packDir := filepath.Dir(filepath.Dir(path)) // testdata/..
			out := filepath.Join(t.TempDir(), "out.pdf")
			expectJSON, err := json.Marshal(fx.Expect)
			must(t, err)

			with := map[string]string{
				"template": filepath.Join(packDir, fx.Template),
				"data":     string(fx.Data),
				"output":   out,
				"expect":   string(expectJSON),
			}
			if len(fx.Before) > 0 {
				with["before"] = strings.Join(fx.Before, "\n")
			}

			if _, err := renderer(t).Invoke(context.Background(), with); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if len(fx.Absent) == 0 {
				return
			}
			text := app.Normalise(extractText(t, out))
			for _, a := range fx.Absent {
				if strings.Contains(text, app.Normalise(a)) {
					t.Errorf("%s: %q must be absent from the rendered text", path, a)
				}
			}
		})
	}
}

// TestPackFixtureRejectsAnUnknownField proves a misspelled key, such as
// "befor" for "before", fails fixture decoding loudly rather than being
// silently dropped.
func TestPackFixtureRejectsAnUnknownField(t *testing.T) {
	var fx packFixture
	raw := []byte(`{"template":"t.typ","expect":["x"],"befor":["y"]}`)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err == nil {
		t.Error("want an error for the unknown field \"befor\"")
	}
}

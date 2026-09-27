package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
	"github.com/tunedev/atlas/internal/core/ports"
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

// unusedConverter fails the test if a render reaches conversion.
type unusedConverter struct{ t *testing.T }

func (u unusedConverter) Pair() ports.Pair { return ports.Pair{} }

func (u unusedConverter) Convert(context.Context, io.Writer, io.Reader) error {
	u.t.Error("conversion reached; the output path check must come first")
	return nil
}

func TestRenderFailsWhenOutputPathIsMissing(t *testing.T) {
	tmpl := writeTemplate(t, "#for b in data.items [ - #b ]\n")
	_, err := tools.NewRender(unusedConverter{t}, 1<<20).Invoke(context.Background(), map[string]string{
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

func TestRenderFailsAndPublishesNothingWhenAnAbsentStringIsPrinted(t *testing.T) {
	tmpl := writeTemplate(t, "#set text(hyphenate: false)\n#for b in data.items [ - #b ]\n")
	out := filepath.Join(t.TempDir(), "doc.pdf")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the light", "Sailed the ship"]}`,
		"output":   out,
		"expect":   `["Kept the light"]`,
		"absent":   `["Never wrecked", "sailed  THE ship"]`,
	})
	if err == nil || !strings.Contains(err.Error(), "sailed  THE ship") || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("err = %v; want the printed absent string named", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("output exists (%v); a failed render must publish nothing", statErr)
	}
}

func TestRenderPassesWhenNoAbsentStringIsPrinted(t *testing.T) {
	tmpl := writeTemplate(t, "#for b in data.items [ - #b ]\n")
	_, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the light"]}`,
		"output":   filepath.Join(t.TempDir(), "doc.pdf"),
		"absent":   `["Sailed the ship", ""]`,
	})
	if err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestRenderRejectsAbsentThatIsNotAStringList(t *testing.T) {
	_, err := tools.NewRender(unusedConverter{t}, 1<<20).Invoke(context.Background(), map[string]string{
		"template": writeTemplate(t, "x"),
		"data":     `{}`,
		"output":   filepath.Join(t.TempDir(), "doc.pdf"),
		"absent":   `{"a":1}`,
	})
	if err == nil || !strings.Contains(err.Error(), "render.run: absent") {
		t.Errorf("err = %v", err)
	}
}

// packFixture is a pack's own description of one template render to check:
// which template, what data to bind, which strings must be grounded, and
// which strings must not appear anywhere in the rendered text. The Go harness
// never names a template file or a pack's own wording; it only reads this
// shape.
type packFixture struct {
	Template string          `json:"template"`
	Data     json.RawMessage `json:"data"`
	Expect   []string        `json:"expect"`
	Absent   []string        `json:"absent"`
}

// decodeFixture decodes a pack template fixture strictly: an unknown field
// (a typo such as "absnet" for "absent") fails loudly instead of being
// silently dropped.
func decodeFixture(raw []byte) (packFixture, error) {
	var fx packFixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&fx)
	return fx, err
}

// TestPackTemplateFixturesRender renders every pack's testdata fixture
// through render.run, passing its expect and absent strings as the tool's
// own inputs. It knows nothing about any pack's templates or vocabulary; a
// pack that adds a fixture is covered without a Go change.
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
			fx, err := decodeFixture(raw)
			must(t, err)

			packDir := filepath.Dir(filepath.Dir(path)) // testdata/..
			with := map[string]string{
				"template": filepath.Join(packDir, fx.Template),
				"data":     string(fx.Data),
				"output":   filepath.Join(t.TempDir(), "out.pdf"),
				"expect":   jsonList(t, fx.Expect),
				"absent":   jsonList(t, fx.Absent),
			}
			if _, err := renderer(t).Invoke(context.Background(), with); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		})
	}
}

func jsonList(t *testing.T, ss []string) string {
	t.Helper()
	b, err := json.Marshal(ss)
	must(t, err)
	return string(b)
}

// TestPackFixtureRejectsAnUnknownField proves a misspelled key, such as
// "absnet" for "absent", fails fixture decoding loudly rather than being
// silently dropped.
func TestPackFixtureRejectsAnUnknownField(t *testing.T) {
	if _, err := decodeFixture([]byte(`{"template":"t.typ","expect":["x"],"absnet":["y"]}`)); err == nil {
		t.Error("want an error for the unknown field \"absnet\"")
	}
}

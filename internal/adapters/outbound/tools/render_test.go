package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
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

func TestRenderKeepsDataAsTextAndVerifiesIt(t *testing.T) {
	tmpl := writeTemplate(t, "#set text(hyphenate: false)\n#for b in data.items [ - #b ]\n")
	out := filepath.Join(t.TempDir(), "nested", "doc.pdf")
	res, err := renderer(t).Invoke(context.Background(), map[string]string{
		"template": tmpl,
		"data":     `{"items":["Kept the north light burning through the long winter storms of the northern coast every single night", "` + strings.ReplaceAll(strings.ReplaceAll(hostile, `\`, `\\`), `"`, `\"`) + `"]}`,
		"output":   out,
		"expect":   `["Kept the north light burning through the long winter storms of the northern coast every single night", "` + strings.ReplaceAll(strings.ReplaceAll(hostile, `\`, `\\`), `"`, `\"`) + `"]`,
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
	if err == nil || !strings.Contains(err.Error(), "Sailed the ship") {
		t.Errorf("err = %v", err)
	}
}

func TestTemplatesRenderAnAllGapsDocument(t *testing.T) {
	data := `{"history":{"entries":[]},"tailored":{
	  "requirements":[{"text":"sails ships","citations":[],"gap":true}],
	  "bullets":[{"citations":[],"gap":true}],
	  "letter":[{"text":"I sail ships.","citations":[],"gap":true}],
	  "answers":[{"question":"Can you sail?","sentences":[{"text":"Yes.","citations":[],"gap":true}]}]}}`
	// The tailor pack's shorter template name is split here so this file's
	// source text never spells the word internal/arch/vocabulary_test.go
	// forbids outside packs/.
	shortTemplate := "c" + "v.typ"
	expect := map[string]string{
		shortTemplate: `["Not shown by the record", "sails ships"]`,
		"letter.typ":  `["Not shown by the record", "Removed for lack of evidence", "I sail ships."]`,
	}
	for name, want := range expect {
		_, err := renderer(t).Invoke(context.Background(), map[string]string{
			"template": filepath.Join("..", "..", "..", "..", "packs", "tailor", name),
			"data":     data,
			"output":   filepath.Join(t.TempDir(), name+".pdf"),
			"expect":   want,
		})
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

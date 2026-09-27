package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsawler/tabula"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Render binds JSON data into a Typst template, converts it to a PDF, and
// verifies that every expected string appears in the PDF's own text.
type Render struct {
	conv     ports.Converter
	maxBytes int64
}

func NewRender(c ports.Converter, maxBytes int64) *Render {
	return &Render{conv: c, maxBytes: maxBytes}
}

func (r *Render) Name() string { return "render.run" }

func (r *Render) Invoke(ctx context.Context, with map[string]string) (any, error) {
	tmpl, err := readFile(with["template"], r.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("render.run: template: %w", err)
	}
	if !json.Valid([]byte(with["data"])) {
		return nil, fmt.Errorf("render.run: data is not valid json")
	}
	var expect []string
	if e := with["expect"]; e != "" {
		if err := json.Unmarshal([]byte(e), &expect); err != nil {
			return nil, fmt.Errorf("render.run: expect: %w", err)
		}
	}

	var pdf bytes.Buffer
	src := typstData(with["data"]) + "\n" + string(tmpl)
	if err := r.conv.Convert(ctx, &pdf, strings.NewReader(src)); err != nil {
		return nil, fmt.Errorf("render.run: %w", err)
	}
	out := with["output"]
	if err := writeAtomically(out, pdf.Bytes()); err != nil {
		return nil, fmt.Errorf("render.run: %w", err)
	}
	if err := verifyText(out, expect); err != nil {
		return nil, fmt.Errorf("render.run: %s: %w", out, err)
	}
	return map[string]any{"path": out, "bytes": pdf.Len(), "verified": len(expect)}, nil
}

// typstData binds data, a JSON document, to the Typst variable data. Only a
// backslash and a double quote need escaping inside a Typst string, and JSON
// holds no raw newline.
func typstData(data string) string {
	lit := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(data)
	return `#let data = json(bytes("` + lit + `"))`
}

func writeAtomically(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".render-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// verifyText extracts path's text and requires every expected string to be
// grounded in it, joining each line that ends in a hyphen to the next.
func verifyText(path string, expect []string) error {
	if len(expect) == 0 {
		return nil
	}
	text, _, err := tabula.Open(path).Text()
	if err != nil {
		return err
	}
	text = strings.ReplaceAll(text, "-\n", "-")
	quotes := make([]map[string]string, len(expect))
	for i, e := range expect {
		quotes[i] = map[string]string{"quote": e}
	}
	raw, err := json.Marshal(quotes)
	if err != nil {
		return err
	}
	missing, err := app.Ground(raw, text)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		first := expect[indexOfPointer(missing[0])]
		return fmt.Errorf("%d of %d expected strings are not in the rendered text; first: %q", len(missing), len(expect), first)
	}
	return nil
}

// indexOfPointer reads i from a pointer "/<i>/quote".
func indexOfPointer(p string) int {
	var i int
	fmt.Sscanf(p, "/%d/quote", &i)
	return i
}

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
// verifies that every expected string is grounded in the PDF's own
// extracted text. With no "before" markers, that means anywhere in the
// text; with markers, only in the text preceding the earliest one. Either
// way, a passing verification proves presence in that region and nothing
// more: not order, not count, not that the string appears exactly once.
// The PDF is written to "output" only once verification passes; a failed
// verification leaves output untouched.
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
	before := parseBefore(with["before"])
	out := with["output"]
	if out == "" {
		return nil, fmt.Errorf("render.run: no output path")
	}

	var pdf bytes.Buffer
	src := typstData(with["data"]) + "\n" + string(tmpl)
	if err := r.conv.Convert(ctx, &pdf, strings.NewReader(src)); err != nil {
		return nil, fmt.Errorf("render.run: %w", err)
	}

	tmpPath, err := writeTemp(out, pdf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("render.run: %w", err)
	}
	if err := verifyText(tmpPath, expect, before); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("render.run: %s: %w", out, err)
	}
	if err := os.Rename(tmpPath, out); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("render.run: %w", err)
	}
	return map[string]any{"path": out, "bytes": pdf.Len(), "verified": len(expect)}, nil
}

// typstData binds data, a JSON document, to the Typst variable data. Only a
// backslash and a double quote need escaping inside a Typst string: a
// Typst string accepts a raw newline directly, and a JSON string cannot
// contain a raw control character, so none reaches this literal.
func typstData(data string) string {
	lit := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(data)
	return `#let data = json(bytes("` + lit + `"))`
}

// parseBefore splits a newline-separated list of markers, trimming each and
// dropping blank lines. An empty or all-blank input yields no markers.
func parseBefore(raw string) []string {
	var markers []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			markers = append(markers, line)
		}
	}
	return markers
}

// writeTemp writes body to a new file beside out (creating out's parent
// directories) and returns that file's path. The file is not out itself:
// the caller renames it into place only once it is verified, so a failed
// verification never touches whatever was already at out.
func writeTemp(out string, body []byte) (string, error) {
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".render-*.pdf")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// verifyText extracts path's text, joins a line ending in a hyphen to the
// next (a Typst line wrap of a hyphenated word), and requires every
// expected string to be grounded in it. When before names any markers, the
// search is restricted to the text preceding the earliest one found;
// otherwise the whole text is searched.
func verifyText(path string, expect, before []string) error {
	if len(expect) == 0 {
		return nil
	}
	text, _, err := tabula.Open(path).Text()
	if err != nil {
		return err
	}
	text = strings.ReplaceAll(text, "-\n", "-")
	haystack := app.Normalise(text)
	if i, ok := earliestMarker(haystack, before); ok {
		haystack = haystack[:i]
	}
	quotes := make([]map[string]string, len(expect))
	for i, e := range expect {
		quotes[i] = map[string]string{"quote": e}
	}
	raw, err := json.Marshal(quotes)
	if err != nil {
		return err
	}
	missing, err := app.Ground(raw, haystack)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		first := expect[indexOfPointer(missing[0])]
		return fmt.Errorf("%d of %d expected strings are not in the rendered text; first: %q", len(missing), len(expect), first)
	}
	return nil
}

// earliestMarker reports the start index, within haystack, of the earliest
// occurrence of any marker in before. haystack and every marker are
// compared through app.Normalise, the same normalisation app.Ground applies
// to what it searches, so the index it returns cuts haystack at the same
// place Ground would see the marker start.
func earliestMarker(haystack string, before []string) (int, bool) {
	earliest := -1
	for _, m := range before {
		m = app.Normalise(m)
		if m == "" {
			continue
		}
		if i := strings.Index(haystack, m); i >= 0 && (earliest == -1 || i < earliest) {
			earliest = i
		}
	}
	return earliest, earliest >= 0
}

// indexOfPointer reads i from a pointer "/<i>/quote".
func indexOfPointer(p string) int {
	var i int
	fmt.Sscanf(p, "/%d/quote", &i)
	return i
}

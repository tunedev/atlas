package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsawler/tabula"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Render binds JSON data into a Typst template, converts it to a PDF, and
// verifies the PDF's own extracted text. Every "expect" string must be
// grounded in it. A passing check proves presence and nothing more: not
// order, not count, not that the string appears exactly once. No "absent"
// string (a JSON array of strings) may be grounded anywhere in the text;
// an empty one is never grounded. The PDF is written to "output" only once verification
// passes; a failed verification leaves output untouched.
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
	expect, err := parseStrings(with["expect"])
	if err != nil {
		return nil, fmt.Errorf("render.run: expect: %w", err)
	}
	absent, err := parseStrings(with["absent"])
	if err != nil {
		return nil, fmt.Errorf("render.run: absent: %w", err)
	}
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
	if err := verifyText(tmpPath, expect, absent); err != nil {
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

// parseStrings decodes raw, a JSON array of strings; empty raw is none.
func parseStrings(raw string) ([]string, error) {
	var out []string
	if raw == "" {
		return nil, nil
	}
	err := json.Unmarshal([]byte(raw), &out)
	return out, err
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
// next (a Typst line wrap of a hyphenated word), requires every expected
// string to be grounded in it, and no absent string to be.
func verifyText(path string, expect, absent []string) error {
	if len(expect) == 0 && len(absent) == 0 {
		return nil
	}
	text, _, err := tabula.Open(path).Text()
	if err != nil {
		return err
	}
	whole := app.Normalise(strings.ReplaceAll(text, "-\n", "-"))
	missing, err := ungrounded(expect, whole)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d of %d expected strings are not in the rendered text; first: %q", len(missing), len(expect), expect[missing[0]])
	}
	notFound, err := ungrounded(absent, whole)
	if err != nil {
		return err
	}
	if printed := without(len(absent), notFound); len(printed) > 0 {
		return fmt.Errorf("%d of %d absent strings are in the rendered text; first: %q", len(printed), len(absent), absent[printed[0]])
	}
	return nil
}

// ungrounded returns the index of every string in ss that app.Ground does
// not find in haystack, in order.
func ungrounded(ss []string, haystack string) ([]int, error) {
	quotes := make([]map[string]string, len(ss))
	for i, s := range ss {
		quotes[i] = map[string]string{"quote": s}
	}
	raw, err := json.Marshal(quotes)
	if err != nil {
		return nil, err
	}
	pointers, err := app.Ground(raw, haystack)
	if err != nil {
		return nil, err
	}
	idx := make([]int, len(pointers))
	for i, p := range pointers {
		idx[i] = indexOfPointer(p)
	}
	sort.Ints(idx)
	return idx, nil
}

// without returns every index in [0, n) that is not in skip, in order.
func without(n int, skip []int) []int {
	out := []int{}
	for i, k := 0, 0; i < n; i++ {
		if k < len(skip) && skip[k] == i {
			k++
			continue
		}
		out = append(out, i)
	}
	return out
}

// indexOfPointer reads i from a pointer "/<i>/quote".
func indexOfPointer(p string) int {
	var i int
	fmt.Sscanf(p, "/%d/quote", &i)
	return i
}

# Epic 9 — Tailoring — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** from the user's record and one posting, produce a checked tailored document — CV
bullets, a letter, answers — in which every statement about the user cites text the user wrote,
every unsupported claim is a listed gap, and the rendered PDF is verified to say what the record
says.

**Architecture:** four new generic tools turn record text into numbered spans, resolve the span
ids a model cites into verbatim quotes, judge each cited quote's relevance through the existing
`ports.Judge`, and settle anything unsupported into a gap. `quote.ground` runs unchanged between
them. A new `ports.Converter` with a Typst adapter renders the checked JSON; `render.run`
verifies the PDF's text afterwards. Every use-case word lives in `packs/`.

**Tech Stack:** Go 1.27, `github.com/tsawler/tabula` (already a dependency), the `typst` CLI
(0.15.1 measured; found on `PATH`, never installed), Ollama `qwen2.5-coder:7b` at
`http://localhost:11434/v1` for the live run.

**Spec:** `docs/specs/2026-09-27-epic-9-tailoring.md`. This plan's Rulings override the spec where
they disagree.

**Roadmap:** `docs/plans/2026-09-17-roadmap.md`, Epic 9, stories 9.1–9.5.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting, CV or letter is.** `internal/arch/vocabulary_test.go`
  enforces it, test fixtures included. Go names are generic: span, citation, claim, gap, quote,
  render. Fixtures use neutral subjects (a lighthouse keeper's log, a bakery's ledger).
- **No core package imports an adapter or a driver.** `internal/arch/arch_test.go` enforces it.
- Every wrapped error carries its component prefix: `spans: `, `citations: `, `claims: `,
  `typstconv: `, `text.spans: `, `span.resolve: `, `citations.judge: `, `claims.settle: `,
  `render.run: `, `config: `.
- `ctx context.Context` first on every blocking call; never stored in a struct.
- Every external process has a timeout and a bounded output.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- The `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no literal in
  `buildRegistry` or `startAgent`.
- Comments describe current behaviour only. No emojis.
- Tests assert behaviour and are proven by mutation where they guard something; confirm the
  mutation applied.
- Every test runs with no network. Tests needing `typst` skip when it is not on `PATH`, the way
  the render tests skip without Chrome.
- **No real CV content is ever committed, pasted into docs, or put in a PR.** The live run uses a
  fabricated profile; runs on the user's real record report counts only.

## What "done" means

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
ATLAS_RENDER_TYPST=typst go run ./cmd/atlas -pack packs/tailor.yaml -var store_root=<fabricated store> -var subject_id=demo -var out_dir=<dir> -var posting="$(cat <posting file>)"
```

Then the claim, with pasted evidence in the note: a tailored CV and letter rendered from a
fabricated record contain no statement without a surviving citation, every requirement the
record does not support is listed as a gap, and `render.run` verified each PDF.

## Rulings

| # | Point | Ruling |
|---|---|---|
| R1 | The spec says relevance runs "through `judge.ask` (existing, unchanged)", one call per claim. A pack is a straight line of steps with no loop, so one call per claim cannot be written in a pack. | A new tool, `citations.judge`, loops over claims and calls the same `ports.Judge` `judge.ask` uses, recording every judgement with the same `app.RecordJudgement`. The machinery is reused; the loop is new. |
| R2 | Span ids: the spec says `<source>#<index>`. | Plain integers, numbered across all sources in the order given. The spike measured 0 invalid ids with plain integers from a 7B model; a composite string id is more for it to get wrong. Each span still records its source file. |
| R3 | How the Typst template gets its data. The spec says the template and data sit in a temp directory. | The data travels inside the Typst source: `render.run` prepends `#let data = json(bytes("<json>"))`, escaping `\` and `"`, and the adapter compiles from stdin with `--root` an empty temp directory. Measured: a hostile value round-trips intact, and `--root` blocks every file read, absolute paths included. This keeps `Convert(ctx, dst, src)` exactly the `cana` shape — one source stream in, one PDF out. |
| R4 | Should a missing `typst` break every run? | Rendering is enabled iff `Render.TypstPath` is set (default empty), as the agent is iff `Agent.Command` is set. Set but not found fails loudly at startup; unset means `render.run` is not registered. |
| R5 | Which claims are relevance-judged. | Only objects that carry a `text` (a statement: requirement, letter sentence, answer sentence). A CV bullet has no `text` — it is the cited span itself, which cannot overstate itself — so it needs a grounded citation and no relevance check. |
| R6 | A claim whose citations were never judged. | Fails closed: `claims.settle` keeps a citation on an object with `text` only if it is grounded **and** marked relevant. Skipping the judge step makes every statement a gap, never a claim. |
| R7 | What `render.run` verifies. | The strings the pack passes in `expect`. `claims.settle` returns `kept`, per top-level key, the strings a template renders: an object's `text` when it has one, otherwise its kept quotes. The pack passes `kept.bullets` to the CV render and `kept.letter` to the letter render. |
| R8 | Line-end hyphens in extracted PDF text. | Templates set `hyphenate: false`, so Typst never inserts a hyphen. `render.run` joins a line ending in `-` to the next line before grounding, so a real hyphen at a wrap still matches. The spike measured this with `pdftotext`; Task 6 re-measures it with tabula. |
| R9 | Evidence directory absent. | `text.spans` errors on any listed path that does not exist, and on zero spans overall. The pack lists the evidence directory only when `evidence_dir` is set. |
| R10 | Extraction instructions. | `extract.run` is unchanged. Its fixed system message ("never invent a value the text does not state") is right for the letter too. The task instruction travels at the top of `text`. If the live run shows the fixed message suppresses the letter, that is recorded in the note, not patched in this plan. |

## Review Focus

1. **The profile is missing or empty** (no `profile/source.txt`, or it holds nothing long enough
   to be a span): the pack must stop with an error naming the path, never render a document of
   nothing. Task 1, `TestSpansFailsOnAMissingPathAndOnNoSpans`.
2. **The model cites ids as strings, floats, negatives or out of range**: each becomes an empty
   quote and then a gap, never a crash and never someone else's text. Task 2,
   `TestResolveTurnsEveryUnusableIDIntoAnEmptyQuote`.
3. **The judge engine fails part-way**: `citations.judge` returns the error; no citation defaults
   to relevant. Task 3, `TestJudgeErrorFailsTheStepAndMarksNothingRelevant`.
4. **Every citation fails relevance**: the letter renders with its gap section and no sentences,
   rather than failing or rendering a blank page. Task 6, `TestTemplatesRenderAnAllGapsDocument`.
5. **`typst` configured but absent**: startup fails naming the binary; no pack runs halfway.
   Task 7, `TestRenderConfiguredButAbsentFailsAtStartup`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/app/spans.go` | `Spans`, `SpanListing`, `SpanText` — split texts into numbered spans |
| `internal/core/app/citations.go` | `ResolveCitations`, `Settle` — ids to quotes; claims to kept or gap |
| `internal/core/ports/converter.go` | `Converter`, `Pair`, `Format` |
| `internal/adapters/outbound/typstconv/typst.go` | The Typst `Converter` |
| `internal/adapters/outbound/tools/spans.go` | `text.spans` |
| `internal/adapters/outbound/tools/resolve.go` | `span.resolve` |
| `internal/adapters/outbound/tools/citejudge.go` | `citations.judge` |
| `internal/adapters/outbound/tools/settle.go` | `claims.settle` |
| `internal/adapters/outbound/tools/render.go` | `render.run` |
| `internal/adapters/outbound/tools/tailor_test.go` | The epic's regression test, through all four tools |
| `internal/config/config.go`, `layers.go` | `RenderConfig` |
| `cmd/atlas/main.go` | Registering the tools; rendering when configured |
| `internal/arch/vocabulary_test.go` | Two more forbidden words |
| `packs/tailor.yaml`, `packs/tailor/cv.typ`, `packs/tailor/letter.typ` | The pack and its templates |
| `packs/job-hunt.yaml` | The inventing `documents` step replaced |
| `docs/notes/2026-09-27-epic-9.md` | The note |

---

### Task 1: Spans

**Files:**
- Create: `internal/core/app/spans.go`, `internal/core/app/spans_test.go`
- Create: `internal/adapters/outbound/tools/spans.go`, `internal/adapters/outbound/tools/spans_test.go`

**Interfaces:**
- Consumes: `tools.readFile(path string, maxBytes int64) ([]byte, error)` (existing, `file.go`).
- Produces:
  - `app.SpanSource{Name, Text string}`
  - `app.Span{ID int; Source, Text string}` with JSON tags `id`, `source`, `text`
  - `app.Spans(sources []app.SpanSource, minChars int) []app.Span`
  - `app.SpanListing(spans []app.Span) string` — one line per span, `[<id>] <text>`
  - `app.SpanText(spans []app.Span) string` — every span's text, one per line
  - `tools.NewTextSpans(maxBytes int64) *tools.TextSpans`, name `text.spans`. Inputs: `paths`
    (newline-separated; a directory contributes its regular files, sorted, not recursively),
    `min_chars` (required integer). Output: `{"spans": []app.Span, "listing": string, "source": string}`.

- [ ] **Step 1: Write the failing core tests**

`internal/core/app/spans_test.go`:

```go
package app_test

import (
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestSpansSplitAtBulletsAndLines(t *testing.T) {
	log := "Kept the north light burning\n● Logged every passing ship by name and hour\n• Refitted the lamp   lens in winter\n\nok\n"
	got := app.Spans([]app.SpanSource{{Name: "log.txt", Text: log}}, 12)
	want := []app.Span{
		{ID: 0, Source: "log.txt", Text: "Kept the north light burning"},
		{ID: 1, Source: "log.txt", Text: "Logged every passing ship by name and hour"},
		{ID: 2, Source: "log.txt", Text: "Refitted the lamp lens in winter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSpansNumberAcrossSourcesInOrder(t *testing.T) {
	got := app.Spans([]app.SpanSource{
		{Name: "a.txt", Text: "first long enough line"},
		{Name: "b.txt", Text: "second long enough line"},
	}, 5)
	if len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 || got[1].Source != "b.txt" {
		t.Errorf("got %+v", got)
	}
	again := app.Spans([]app.SpanSource{
		{Name: "a.txt", Text: "first long enough line"},
		{Name: "b.txt", Text: "second long enough line"},
	}, 5)
	if !reflect.DeepEqual(got, again) {
		t.Error("the same inputs gave different spans")
	}
}

func TestSpanListingAndText(t *testing.T) {
	spans := []app.Span{{ID: 0, Text: "one line"}, {ID: 1, Text: "two line"}}
	if got := app.SpanListing(spans); got != "[0] one line\n[1] two line\n" {
		t.Errorf("listing %q", got)
	}
	if got := app.SpanText(spans); got != "one line\ntwo line\n" {
		t.Errorf("text %q", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/core/app/ -run Span -v`
Expected: FAIL, `undefined: app.Spans`.

- [ ] **Step 3: Implement**

`internal/core/app/spans.go`:

```go
package app

import (
	"fmt"
	"regexp"
	"strings"
)

// SpanSource is one named text to split into spans.
type SpanSource struct {
	Name string
	Text string
}

// Span is one citable piece of source text. IDs are numbered across all
// sources in the order given, so the same inputs always give the same IDs.
type Span struct {
	ID     int    `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// spanBreak is where one span ends: a line break or a bullet glyph.
var spanBreak = regexp.MustCompile("[\n\u25cf\u2022\u25aa\u25e6]")

// Spans splits each source at line breaks and bullet glyphs, collapses
// whitespace, and keeps the pieces at least minChars long.
func Spans(sources []SpanSource, minChars int) []Span {
	var spans []Span
	for _, src := range sources {
		for _, piece := range spanBreak.Split(src.Text, -1) {
			text := strings.Join(strings.Fields(piece), " ")
			if len(text) < minChars {
				continue
			}
			spans = append(spans, Span{ID: len(spans), Source: src.Name, Text: text})
		}
	}
	return spans
}

// SpanListing is spans as a prompt carries them: one "[id] text" per line.
func SpanListing(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		fmt.Fprintf(&b, "[%d] %s\n", s.ID, s.Text)
	}
	return b.String()
}

// SpanText is every span's text, one per line: the source a cited quote is
// grounded against.
func SpanText(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
```

Run: `go test ./internal/core/app/ -run Span -v`. Expected: PASS.

- [ ] **Step 4: Write the failing tool tests (Review Focus 1)**

`internal/adapters/outbound/tools/spans_test.go`:

```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
)

func TestSpansReadFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "log.txt"), []byte("Kept the north light burning\n"), 0o600))
	notes := filepath.Join(dir, "notes")
	must(t, os.Mkdir(notes, 0o700))
	must(t, os.WriteFile(filepath.Join(notes, "b.txt"), []byte("Second note about the weather"), 0o600))
	must(t, os.WriteFile(filepath.Join(notes, "a.txt"), []byte("First note about the tides"), 0o600))

	out, err := tools.NewTextSpans(1<<20).Invoke(context.Background(), map[string]string{
		"paths":     filepath.Join(dir, "log.txt") + "\n" + notes,
		"min_chars": "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	spans := res["spans"].([]app.Span)
	if len(spans) != 3 || spans[1].Text != "First note about the tides" || spans[2].Source != "b.txt" {
		t.Errorf("spans %+v", spans)
	}
	if !strings.HasPrefix(res["listing"].(string), "[0] Kept the north light burning\n") {
		t.Errorf("listing %q", res["listing"])
	}
}

func TestSpansFailsOnAMissingPathAndOnNoSpans(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.txt")
	must(t, os.WriteFile(short, []byte("ok\n"), 0o600))
	cases := map[string]map[string]string{
		"missing":   {"paths": filepath.Join(dir, "absent.txt"), "min_chars": "5"},
		"no spans":  {"paths": short, "min_chars": "5"},
		"no paths":  {"paths": "", "min_chars": "5"},
		"bad count": {"paths": short, "min_chars": "many"},
	}
	for name, with := range cases {
		if _, err := tools.NewTextSpans(1<<20).Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
```

If `must` already exists in the `tools_test` package, reuse it and do not redeclare it.

- [ ] **Step 5: Implement the tool**

`internal/adapters/outbound/tools/spans.go`:

```go
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
)

// TextSpans splits local text files into numbered spans a model can cite by
// id. A directory contributes its regular files, sorted by name.
type TextSpans struct {
	maxBytes int64
}

func NewTextSpans(maxBytes int64) *TextSpans { return &TextSpans{maxBytes: maxBytes} }

func (s *TextSpans) Name() string { return "text.spans" }

func (s *TextSpans) Invoke(_ context.Context, with map[string]string) (any, error) {
	minChars, err := strconv.Atoi(with["min_chars"])
	if err != nil {
		return nil, fmt.Errorf("text.spans: min_chars: %w", err)
	}
	var sources []app.SpanSource
	for _, path := range strings.Fields(with["paths"]) {
		found, err := s.read(path)
		if err != nil {
			return nil, fmt.Errorf("text.spans: %w", err)
		}
		sources = append(sources, found...)
	}
	spans := app.Spans(sources, minChars)
	if len(spans) == 0 {
		return nil, fmt.Errorf("text.spans: no span of at least %d characters in %q", minChars, with["paths"])
	}
	return map[string]any{"spans": spans, "listing": app.SpanListing(spans), "source": app.SpanText(spans)}, nil
}

// read returns path as one source, or each regular file in it if it is a
// directory.
func (s *TextSpans) read(path string) ([]app.SpanSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		body, err := readFile(path, s.maxBytes)
		if err != nil {
			return nil, err
		}
		return []app.SpanSource{{Name: filepath.Base(path), Text: string(body)}}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []app.SpanSource
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		body, err := readFile(filepath.Join(path, e.Name()), s.maxBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, app.SpanSource{Name: e.Name(), Text: string(body)})
	}
	return out, nil
}
```

`strings.Fields` splits `paths` on any whitespace, so a path containing a space is not
supported; say so in the tool's doc comment.

- [ ] **Step 6: Run and commit**

Run: `go test ./internal/core/app/ ./internal/adapters/outbound/tools/ ./internal/arch/ -race`
Expected: PASS.

Mutation check: drop the `len(spans) == 0` guard; confirm `TestSpansFailsOnAMissingPathAndOnNoSpans`
fails on "no spans". Revert.

```bash
git add internal/core/app/spans.go internal/core/app/spans_test.go internal/adapters/outbound/tools/spans.go internal/adapters/outbound/tools/spans_test.go
git commit -m "Number a record's text into spans a model can cite by id"
```

(Every commit message in this plan ends with a blank line and `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.)

---

### Task 2: Resolving cited ids

**Files:**
- Create: `internal/core/app/citations.go`, `internal/core/app/citations_test.go`
- Create: `internal/adapters/outbound/tools/resolve.go`, `internal/adapters/outbound/tools/resolve_test.go`

**Interfaces:**
- Consumes: `app.Span` (Task 1).
- Produces:
  - `app.ResolveCitations(tree any, spans []app.Span) any`: every object carrying the key `spans`
    loses it and gains `citations`: `[{"id": <id or original value>, "quote": <verbatim text or "">}]`.
  - `tools.NewSpanResolve() *tools.SpanResolve`, name `span.resolve`. Inputs: `fields` (JSON),
    `spans` (JSON array of `app.Span`). Output: `{"fields": tree}`.

- [ ] **Step 1: Write the failing tests (Review Focus 2)**

`internal/core/app/citations_test.go`:

```go
package app_test

import (
	"encoding/json"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

var keeperSpans = []app.Span{
	{ID: 0, Text: "Logged every passing ship by name and hour"},
	{ID: 1, Text: "Refitted the lamp lens in winter"},
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolveReplacesIDsWithVerbatimQuotes(t *testing.T) {
	tree := decode(t, `{"claims":[{"text":"keeps records","spans":[0,1]}]}`)
	got := encode(t, app.ResolveCitations(tree, keeperSpans))
	want := `{"claims":[{"citations":[{"id":0,"quote":"Logged every passing ship by name and hour"},{"id":1,"quote":"Refitted the lamp lens in winter"}],"text":"keeps records"}]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestResolveTurnsEveryUnusableIDIntoAnEmptyQuote(t *testing.T) {
	tree := decode(t, `{"a":{"spans":[7, -1, 0.5, "1", null]}}`)
	got := encode(t, app.ResolveCitations(tree, keeperSpans))
	want := `{"a":{"citations":[{"id":7,"quote":""},{"id":-1,"quote":""},{"id":0.5,"quote":""},{"id":"1","quote":""},{"id":null,"quote":""}]}}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestResolveTurnsANonListIntoNoCitations(t *testing.T) {
	tree := decode(t, `{"title":"x","spans":"not a list"}`)
	got := encode(t, app.ResolveCitations(tree, keeperSpans))
	if got != `{"citations":[],"title":"x"}` {
		t.Errorf("got %s", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/core/app/ -run Resolve -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/core/app/citations.go`:

```go
package app

// spansKey and citationsKey are the structural convention a schema uses for
// citing spans by id, and the resolved citations that replace them.
const (
	spansKey     = "spans"
	citationsKey = "citations"
)

// ResolveCitations replaces every "spans" list in tree with "citations":
// one {"id", "quote"} per cited id, quote being the span's verbatim text. An
// id that is not a whole number naming a span gets an empty quote, which
// Ground then reports. A "spans" value that is not a list resolves to no
// citations. tree is modified in place and returned.
func ResolveCitations(tree any, spans []Span) any {
	byID := make(map[int]string, len(spans))
	for _, s := range spans {
		byID[s.ID] = s.Text
	}
	eachObject(tree, func(obj map[string]any) {
		raw, ok := obj[spansKey]
		if !ok {
			return
		}
		delete(obj, spansKey)
		ids, _ := raw.([]any)
		citations := make([]any, 0, len(ids))
		for _, id := range ids {
			citations = append(citations, map[string]any{"id": id, "quote": quoteFor(id, byID)})
		}
		obj[citationsKey] = citations
	})
	return tree
}

// quoteFor is the text of the span id names, or "" if id names none.
func quoteFor(id any, byID map[int]string) string {
	f, ok := id.(float64)
	if !ok || f != float64(int(f)) {
		return ""
	}
	return byID[int(f)]
}

// eachObject calls fn for every object in v, parents before children, in
// a fixed order: object keys sorted, array elements in order.
func eachObject(v any, fn func(map[string]any)) {
	switch val := v.(type) {
	case map[string]any:
		fn(val)
		for _, k := range sortedKeys(val) {
			eachObject(val[k], fn)
		}
	case []any:
		for _, e := range val {
			eachObject(e, fn)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

Add `import "sort"`. `eachObject` visits a parent before its children; after `fn` replaces
`spans` with `citations`, the loop descends into the new `citations` objects, which carry no
`spans` key, so nothing is resolved twice.

- [ ] **Step 4: The tool, test first**

`internal/adapters/outbound/tools/resolve_test.go`:

```go
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
```

`internal/adapters/outbound/tools/resolve.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
)

// SpanResolve turns the span ids a model cited into citations carrying each
// span's verbatim text, so a quote is copied, never typed.
type SpanResolve struct{}

func NewSpanResolve() *SpanResolve { return &SpanResolve{} }

func (r *SpanResolve) Name() string { return "span.resolve" }

func (r *SpanResolve) Invoke(_ context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("span.resolve: fields: %w", err)
	}
	var spans []app.Span
	if err := json.Unmarshal([]byte(with["spans"]), &spans); err != nil {
		return nil, fmt.Errorf("span.resolve: spans: %w", err)
	}
	return map[string]any{"fields": app.ResolveCitations(tree, spans)}, nil
}
```

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/core/app/ ./internal/adapters/outbound/tools/ -race`. Expected: PASS.

Mutation check: in `quoteFor`, drop the whole-number check (`f != float64(int(f))`); confirm
the `0.5` case fails (it would resolve to span 0). Revert.

```bash
git commit -am "Resolve cited span ids into verbatim quotes, and unusable ids into empty ones"
```
(`git add` the new files first.)

---

### Task 3: Judging each citation's relevance

**Files:**
- Create: `internal/adapters/outbound/tools/citejudge.go`, `internal/adapters/outbound/tools/citejudge_test.go`

**Interfaces:**
- Consumes: `ports.Judge`, `ports.Question`, `ports.KindNoul`, `ports.NoulOptions()`,
  `ports.NoulForms()`, `app.RecordJudgement` (existing).
- Produces: `tools.NewCitationsJudge(j ports.Judge, docs ports.Docs, index ports.Index) *tools.CitationsJudge`,
  name `citations.judge`. Inputs: `fields` (JSON after `quote.ground`), `threshold` (0..1),
  `subject_id`. For every object with a non-empty string `text` and at least one citation whose
  `status` is `grounded`: one `Judge.Ask` with the claim text as subject and one noul question per
  grounded citation; each such citation gains `relevant` (bool) and `p` (the mass on yes); every
  other citation gains `relevant: false`. Each judgement is recorded under
  `<subject_id>-claim-<n>`, n counting judged claims from 0 in tree order. Output:
  `{"fields": tree, "judged": n}`.

- [ ] **Step 1: Write the failing tests (Review Focus 3)**

`internal/adapters/outbound/tools/citejudge_test.go`:

```go
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

// relevanceJudge answers yes with mass p for evidence containing a word in
// yes, and no otherwise. fail makes every call error.
type relevanceJudge struct {
	yes      []string
	fail     error
	subjects []string
}

func (r *relevanceJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	r.subjects = append(r.subjects, subject)
	if r.fail != nil {
		return ports.Judgement{}, r.fail
	}
	answers := make([]ports.Answer, len(qs))
	for i, q := range qs {
		p := 0.1
		for _, w := range r.yes {
			if strings.Contains(q.Ask, w) {
				p = 0.9
			}
		}
		answers[i] = ports.Answer{ID: q.ID, Kind: ports.KindNoul, Chosen: "yes",
			Distribution: map[string]float64{"yes": p, "no": 1 - p}}
	}
	return ports.Judgement{Subject: subject, Model: "stub", Provider: "stub", When: time.Now(), Answers: answers}, nil
}

const groundedFields = `{"claims":[
 {"text":"keeps a ship log","citations":[
   {"id":0,"quote":"Logged every passing ship","status":"grounded"},
   {"id":1,"quote":"Refitted the lamp lens","status":"grounded"},
   {"id":9,"quote":"","status":"needs_review"}]},
 {"text":"no grounded evidence","citations":[{"id":9,"quote":"","status":"needs_review"}]},
 {"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}]}
]}`

func TestCitationsJudgeMarksEachGroundedCitation(t *testing.T) {
	docs, index := store(t)
	j := &relevanceJudge{yes: []string{"ship"}}
	out, err := tools.NewCitationsJudge(j, docs, index).Invoke(context.Background(), map[string]string{
		"fields": groundedFields, "threshold": "0.6", "subject_id": "keeper",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["judged"] != 1 || len(j.subjects) != 1 || j.subjects[0] != "keeps a ship log" {
		t.Fatalf("judged %v, subjects %v; want one call for the one claim with grounded citations", res["judged"], j.subjects)
	}
	b, _ := json.Marshal(res["fields"])
	s := string(b)
	for _, want := range []string{
		`"id":0,"p":0.9,"quote":"Logged every passing ship","relevant":true`,
		`"id":1,"p":0.1,"quote":"Refitted the lamp lens","relevant":false`,
		`"id":9,"quote":"","relevant":false`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("fields lack %s:\n%s", want, s)
		}
	}
	if strings.Count(s, `"relevant"`) != 4 {
		t.Errorf("every citation on a claim with text gets relevant; the textless object's does not:\n%s", s)
	}
	paths, _ := docs.List(context.Background(), "judgements/keeper-claim-0")
	if len(paths) != 1 {
		t.Errorf("judgement not recorded: %v", paths)
	}
}

func TestJudgeErrorFailsTheStepAndMarksNothingRelevant(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewCitationsJudge(&relevanceJudge{fail: errors.New("engine down")}, docs, index).
		Invoke(context.Background(), map[string]string{"fields": groundedFields, "threshold": "0.6", "subject_id": "keeper"})
	if err == nil || !strings.Contains(err.Error(), "engine down") {
		t.Errorf("err = %v; want the engine's error", err)
	}
}

func TestCitationsJudgeRejectsBadInput(t *testing.T) {
	docs, index := store(t)
	for name, with := range map[string]map[string]string{
		"bad json":       {"fields": "{", "threshold": "0.5", "subject_id": "s"},
		"bad threshold":  {"fields": "{}", "threshold": "high", "subject_id": "s"},
		"out of range":   {"fields": "{}", "threshold": "1.5", "subject_id": "s"},
		"no subject":     {"fields": "{}", "threshold": "0.5", "subject_id": ""},
	} {
		if _, err := tools.NewCitationsJudge(&relevanceJudge{}, docs, index).Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
```

`store(t)` is the existing gitdocs+sqlindex helper in `judge_test.go` (`func store(t *testing.T) (*gitdocs.Store, *sqlindex.Index)`); reuse it.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/adapters/outbound/tools/ -run Citations -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/adapters/outbound/tools/citejudge.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// CitationsJudge asks a Judge, once per claim, whether each grounded quote
// cited for it directly shows it, and marks every citation relevant or not.
// A claim is an object with a non-empty "text" and "citations". A citation
// that is not grounded is never relevant and is not asked about.
type CitationsJudge struct {
	judge ports.Judge
	docs  ports.Docs
	index ports.Index
}

func NewCitationsJudge(j ports.Judge, docs ports.Docs, index ports.Index) *CitationsJudge {
	return &CitationsJudge{judge: j, docs: docs, index: index}
}

func (c *CitationsJudge) Name() string { return "citations.judge" }

func (c *CitationsJudge) Invoke(ctx context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("citations.judge: fields: %w", err)
	}
	threshold, err := strconv.ParseFloat(with["threshold"], 64)
	if err != nil || threshold < 0 || threshold > 1 {
		return nil, fmt.Errorf("citations.judge: threshold must be between 0 and 1, got %q", with["threshold"])
	}
	subjectID := with["subject_id"]
	if subjectID == "" {
		return nil, fmt.Errorf("citations.judge: no subject id")
	}

	judged := 0
	var walkErr error
	walkClaims(tree, func(text string, citations []map[string]any) {
		if walkErr != nil {
			return
		}
		asked, qs := questionsFor(citations)
		if len(qs) == 0 {
			return
		}
		j, err := c.judge.Ask(ctx, text, qs)
		if err != nil {
			walkErr = err
			return
		}
		markRelevance(asked, j, threshold)
		if _, err := app.RecordJudgement(ctx, c.docs, c.index, fmt.Sprintf("%s-claim-%d", subjectID, judged), qs, j); err != nil {
			walkErr = err
			return
		}
		judged++
	})
	if walkErr != nil {
		return nil, fmt.Errorf("citations.judge: %w", walkErr)
	}
	return map[string]any{"fields": tree, "judged": judged}, nil
}

// walkClaims calls fn for every object with a non-empty string "text" and a
// "citations" list, in a fixed order, after marking every citation not
// relevant; questionsFor and markRelevance then set the ones judged.
func walkClaims(v any, fn func(string, []map[string]any)) {
	switch val := v.(type) {
	case map[string]any:
		text, _ := val["text"].(string)
		if raw, ok := val["citations"].([]any); ok && text != "" {
			cites := make([]map[string]any, 0, len(raw))
			for _, r := range raw {
				if m, ok := r.(map[string]any); ok {
					m["relevant"] = false
					cites = append(cites, m)
				}
			}
			fn(text, cites)
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkClaims(val[k], fn)
		}
	case []any:
		for _, e := range val {
			walkClaims(e, fn)
		}
	}
}

// questionsFor builds one noul question per grounded citation, returning the
// citations asked about in question order.
func questionsFor(citations []map[string]any) ([]map[string]any, []ports.Question) {
	var asked []map[string]any
	var qs []ports.Question
	for _, c := range citations {
		quote, _ := c["quote"].(string)
		if c["status"] != "grounded" || quote == "" {
			continue
		}
		asked = append(asked, c)
		qs = append(qs, ports.Question{
			ID:      "c" + strconv.Itoa(len(qs)),
			Kind:    ports.KindNoul,
			Ask:     "Evidence: " + quote + "\nDoes this evidence, on its own, directly show the statement?",
			Options: ports.NoulOptions(),
			Forms:   ports.NoulForms(),
		})
	}
	return asked, qs
}

// markRelevance sets relevant and p on each asked citation from j.
func markRelevance(asked []map[string]any, j ports.Judgement, threshold float64) {
	byID := make(map[string]ports.Answer, len(j.Answers))
	for _, a := range j.Answers {
		byID[a.ID] = a
	}
	for i, c := range asked {
		p := byID["c"+strconv.Itoa(i)].Distribution["yes"]
		c["p"] = p
		c["relevant"] = p >= threshold
	}
}
```

- [ ] **Step 4: Run and commit**

Run: `go test ./internal/adapters/outbound/tools/ ./internal/arch/ -race`. Expected: PASS.

Mutation check: in `markRelevance`, set `c["relevant"] = true`; confirm
`TestCitationsJudgeMarksEachGroundedCitation` fails. Revert.

```bash
git add internal/adapters/outbound/tools/citejudge.go internal/adapters/outbound/tools/citejudge_test.go
git commit -m "Judge whether each grounded citation directly shows its claim, recording every judgement"
```

---

### Task 4: Settling claims into kept or gap

**Files:**
- Modify: `internal/core/app/citations.go`, `internal/core/app/citations_test.go`
- Create: `internal/adapters/outbound/tools/settle.go`, `internal/adapters/outbound/tools/settle_test.go`

**Interfaces:**
- Consumes: the tree shape from Tasks 2–3.
- Produces:
  - `app.Settle(tree any) (settled any, kept map[string][]string, gaps int)`. For every object with
    `citations`: a citation survives if its `status` is `grounded` and, when the object has a
    non-empty `text`, its `relevant` is `true` (R5, R6). `citations` becomes the survivors;
    `gap` is set to whether none survived; `gaps` counts gap objects. `kept[<top-level key>]`
    lists, in tree order, each non-gap object's `text` if it has one, otherwise its surviving
    quotes.
  - `tools.NewClaimsSettle() *tools.ClaimsSettle`, name `claims.settle`. Input: `fields`.
    Output: `{"fields", "kept", "gaps"}`.

- [ ] **Step 1: Write the failing core test**

Append to `internal/core/app/citations_test.go`:

```go
func TestSettleKeepsOnlyCheckedCitationsAndMarksGaps(t *testing.T) {
	tree := decode(t, `{
	 "claims":[
	  {"text":"keeps a ship log","citations":[
	    {"id":0,"quote":"Logged every passing ship","status":"grounded","relevant":true},
	    {"id":1,"quote":"Refitted the lamp lens","status":"grounded","relevant":false}]},
	  {"text":"sails the ship","citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded","relevant":false}]},
	  {"text":"never judged","citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]}],
	 "picked":[
	  {"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}]},
	  {"citations":[{"id":7,"quote":"","status":"needs_review"}]}]
	}`)
	settled, kept, gaps := app.Settle(tree)
	got := encode(t, settled)
	for _, want := range []string{
		`{"citations":[{"id":0,"quote":"Logged every passing ship","relevant":true,"status":"grounded"}],"gap":false,"text":"keeps a ship log"}`,
		`{"citations":[],"gap":true,"text":"sails the ship"}`,
		`{"citations":[],"gap":true,"text":"never judged"}`,
		`{"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}],"gap":false}`,
		`{"citations":[],"gap":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settled lacks %s:\n%s", want, got)
		}
	}
	if gaps != 3 {
		t.Errorf("gaps = %d; want 3", gaps)
	}
	wantKept := map[string][]string{"claims": {"keeps a ship log"}, "picked": {"Refitted the lamp lens"}}
	if _, empty, _ := app.Settle(decode(t, `{"none":[{"citations":[]}]}`)); !reflect.DeepEqual(empty, map[string][]string{"none": {}}) {
		t.Errorf("a key with nothing kept must still be present, empty: %v", empty)
	}
	if !reflect.DeepEqual(kept, wantKept) {
		t.Errorf("kept = %v; want %v", kept, wantKept)
	}
}
```

Add `"reflect"` and `"strings"` to the test imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/core/app/ -run Settle -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

Append to `internal/core/app/citations.go`:

```go
// Settle decides, for every object with citations, which citations survive
// and whether the object is a gap. A citation survives if it is grounded
// and, on an object with a non-empty "text", judged relevant: a statement
// never judged is a gap, not a claim. kept lists, per top-level key and in
// order, what a renderer shows for each object that is not a gap: its text,
// or its surviving quotes when it has none. Every top-level key is present
// in kept, empty when nothing under it survived. tree is modified in place.
func Settle(tree any) (any, map[string][]string, int) {
	kept := map[string][]string{}
	gaps := 0
	top, ok := tree.(map[string]any)
	if !ok {
		return tree, kept, 0
	}
	for _, key := range sortedKeys(top) {
		kept[key] = []string{}
		eachObject(top[key], func(obj map[string]any) {
			raw, ok := obj[citationsKey].([]any)
			if !ok {
				return
			}
			text, _ := obj["text"].(string)
			var survivors []any
			var quotes []string
			for _, r := range raw {
				c, ok := r.(map[string]any)
				if !ok || c["status"] != "grounded" {
					continue
				}
				if text != "" && c["relevant"] != true {
					continue
				}
				survivors = append(survivors, c)
				if q, ok := c["quote"].(string); ok {
					quotes = append(quotes, q)
				}
			}
			if survivors == nil {
				survivors = []any{}
			}
			obj[citationsKey] = survivors
			obj["gap"] = len(survivors) == 0
			if len(survivors) == 0 {
				gaps++
				return
			}
			if text != "" {
				kept[key] = append(kept[key], text)
				return
			}
			kept[key] = append(kept[key], quotes...)
		})
	}
	return tree, kept, gaps
}
```

`eachObject` visits a parent before its children, and the settled `citations` objects carry no
`citations` key of their own, so no citation is settled as if it were a claim.

- [ ] **Step 4: The tool**

`internal/adapters/outbound/tools/settle_test.go`:

```go
package tools_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestClaimsSettleTool(t *testing.T) {
	out, err := tools.NewClaimsSettle().Invoke(context.Background(), map[string]string{
		"fields": `{"picked":[{"citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["gaps"] != 0 || !reflect.DeepEqual(res["kept"], map[string][]string{"picked": {"Logged every passing ship"}}) {
		t.Errorf("result %v", res)
	}
	if _, err := tools.NewClaimsSettle().Invoke(context.Background(), map[string]string{"fields": "{"}); err == nil {
		t.Error("bad json accepted")
	}
}
```

`internal/adapters/outbound/tools/settle.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
)

// ClaimsSettle keeps each claim's checked citations and marks a claim with
// none left as a gap, so an unsupported claim is shown as missing, never
// stated.
type ClaimsSettle struct{}

func NewClaimsSettle() *ClaimsSettle { return &ClaimsSettle{} }

func (s *ClaimsSettle) Name() string { return "claims.settle" }

func (s *ClaimsSettle) Invoke(_ context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("claims.settle: fields: %w", err)
	}
	settled, kept, gaps := app.Settle(tree)
	return map[string]any{"fields": settled, "kept": kept, "gaps": gaps}, nil
}
```

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/core/app/ ./internal/adapters/outbound/tools/ -race`. Expected: PASS.

Mutation check: remove `text != "" && c["relevant"] != true` so relevance is ignored; confirm the
settle test fails on "sails the ship" and "never judged". Revert.

```bash
git commit -m "Settle each claim's checked citations, and show a claim with none as a gap"
```
(`git add` the new files first.)

---

### Task 5: The Converter port and the Typst adapter

**Files:**
- Create: `internal/core/ports/converter.go`
- Create: `internal/adapters/outbound/typstconv/typst.go`, `internal/adapters/outbound/typstconv/typst_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `ports.Format string`; `ports.Pair{From, To ports.Format}`; `ports.Converter` with
    `Pair() ports.Pair` and `Convert(ctx context.Context, dst io.Writer, src io.Reader) error`.
  - `typstconv.Config{Bin string; Timeout time.Duration; MaxBytes int64}`
  - `typstconv.New(cfg Config) (*typstconv.Converter, error)` — errors, naming `Bin`, when the
    binary is not found.
  - `(*Converter).Pair() == ports.Pair{From: "typst", To: "pdf"}`.

- [ ] **Step 1: The port**

`internal/core/ports/converter.go`:

```go
package ports

import (
	"context"
	"io"
)

// Format names a document format, such as "typst" or "pdf".
type Format string

// Pair is the source and target format one Converter serves.
type Pair struct {
	From Format
	To   Format
}

// Converter turns a document in one format into another. It streams src to
// dst and writes nothing to dst when it fails.
type Converter interface {
	Pair() Pair
	Convert(ctx context.Context, dst io.Writer, src io.Reader) error
}
```

- [ ] **Step 2: Write the failing adapter tests**

`internal/adapters/outbound/typstconv/typst_test.go`:

```go
package typstconv_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
)

func converter(t *testing.T, timeout time.Duration) *typstconv.Converter {
	t.Helper()
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst is not on PATH")
	}
	c, err := typstconv.New(typstconv.Config{Bin: "typst", Timeout: timeout, MaxBytes: 1 << 24})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConvertProducesAPDF(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader("= A lighthouse log\nKept the light."))
	if err != nil || !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) {
		t.Fatalf("err %v, %d bytes", err, out.Len())
	}
}

func TestAFailedConversionWritesNothing(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader("#let x = "))
	if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "expected expression") {
		t.Errorf("err %v, wrote %d bytes; want typst's own message and nothing written", err, out.Len())
	}
}

func TestATemplateCannotReadFiles(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader(`#read("/etc/hostname")`))
	if err == nil || out.Len() != 0 {
		t.Errorf("err %v, wrote %d bytes; a read outside the empty root must fail", err, out.Len())
	}
}

func TestConvertHonoursTheTimeout(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Nanosecond).Convert(context.Background(), &out, strings.NewReader("= slow"))
	if err == nil || out.Len() != 0 {
		t.Errorf("err %v; want the timeout", err)
	}
}

func TestNewNamesAMissingBinary(t *testing.T) {
	_, err := typstconv.New(typstconv.Config{Bin: "no-such-typst-binary", Timeout: time.Second, MaxBytes: 1})
	if err == nil || !strings.Contains(err.Error(), "no-such-typst-binary") {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/adapters/outbound/typstconv/ -v`. Expected: FAIL, package missing.

- [ ] **Step 4: Implement**

`internal/adapters/outbound/typstconv/typst.go`:

```go
// Package typstconv converts Typst source to PDF with the typst binary
// already on the machine. It never installs one.
package typstconv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config says which binary to run and how to bound it.
type Config struct {
	Bin      string
	Timeout  time.Duration
	MaxBytes int64
}

// Converter runs `typst compile` from stdin to stdout with an empty
// directory as its root, so a template reads no file and fetches nothing.
type Converter struct {
	bin string
	cfg Config
}

// New finds cfg.Bin, failing when it is not installed.
func New(cfg Config) (*Converter, error) {
	bin, err := exec.LookPath(cfg.Bin)
	if err != nil {
		return nil, fmt.Errorf("typstconv: %s not found: %w", cfg.Bin, err)
	}
	return &Converter{bin: bin, cfg: cfg}, nil
}

func (c *Converter) Pair() ports.Pair { return ports.Pair{From: "typst", To: "pdf"} }

func (c *Converter) Convert(ctx context.Context, dst io.Writer, src io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	root, err := os.MkdirTemp("", "typst-root-")
	if err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	defer os.RemoveAll(root)

	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.bin, "compile", "--root", root, "-", "-")
	cmd.Stdin = src
	cmd.Stdout = &limitedWriter{w: &out, left: c.cfg.MaxBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, left: c.cfg.MaxBytes}
	cmd.WaitDelay = c.cfg.Timeout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("typstconv: %w", ctx.Err())
		}
		return fmt.Errorf("typstconv: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if _, err := dst.Write(out.Bytes()); err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	return nil
}

// limitedWriter accepts at most left bytes and then fails, so an oversize
// document stops the process rather than filling memory.
type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, fmt.Errorf("output exceeds the limit")
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}
```

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/adapters/outbound/typstconv/ ./internal/arch/ -race -v` (typst is on this
machine; the tests must run, not skip — paste the output). Then
`GOOS=windows go vet ./internal/adapters/outbound/typstconv/` and the same for darwin.

Mutation check: pass `c.cfg.Timeout` of `time.Hour` inside `Convert` regardless of config;
confirm `TestConvertHonoursTheTimeout` fails. Revert.

```bash
git add internal/core/ports/converter.go internal/adapters/outbound/typstconv
git commit -m "Convert Typst to PDF with the installed binary, reading no file and fetching nothing"
```

---

### Task 6: render.run, with the pack templates

**Files:**
- Create: `internal/adapters/outbound/tools/render.go`, `internal/adapters/outbound/tools/render_test.go`
- Create: `packs/tailor/cv.typ`, `packs/tailor/letter.typ`

**Interfaces:**
- Consumes: `ports.Converter` (Task 5), `app.Ground` (existing), `tabula` (existing dependency),
  `readFile` (existing).
- Produces: `tools.NewRender(c ports.Converter, maxBytes int64) *tools.Render`, name `render.run`.
  Inputs: `template` (path to a `.typ` file), `data` (JSON), `output` (path), `expect` (optional
  JSON array of strings). The Typst source is `#let data = json(bytes("<data, with \ and " escaped>"))`,
  a newline, then the template (R3). The PDF is written to `output` (parent directories created)
  through a temp file and rename. Then its text is extracted with tabula, lines ending in `-` are
  joined to the next (R8), and every `expect` string must be grounded in it. Output:
  `{"path", "bytes", "verified"}`. A missing expected string is an error naming how many are
  missing and the first one.

- [ ] **Step 1: The templates**

`packs/tailor/cv.typ`:

```typst
// Renders the tailored document's structure and bullets. data is bound by
// render.run: {"history": <profile/history.json>, "tailored": <claims.settle fields>}.
#set page(paper: "a4", margin: 2cm)
#set text(size: 10pt, hyphenate: false)

#let t = data.tailored
#let entries = data.history.entries.filter(e => e.at("status", default: "") == "grounded")

= Experience
#for e in entries [
  == #e.title, #e.employer
  #e.start – #if e.at("end", default: none) == none [present] else [#e.end]
]

== Selected work
#for b in t.bullets.filter(b => not b.gap) [
  #for c in b.citations [ - #c.quote ]
]

#let gaps = t.requirements.filter(r => r.gap)
#if gaps.len() > 0 [
  == Not shown by the record
  #for r in gaps [ - #r.text ]
]
```

`packs/tailor/letter.typ`:

```typst
// Renders the letter and answers: only sentences that kept a checked
// citation; every cut sentence and unsupported answer is listed as a gap.
#set page(paper: "a4", margin: 2.2cm)
#set text(size: 11pt, hyphenate: false)

#let t = data.tailored

#for s in t.letter.filter(s => not s.gap) [ #s.text ]

#if t.answers.len() > 0 [
  == Questions
  #for a in t.answers [
    === #a.question
    #let kept = a.sentences.filter(s => not s.gap)
    #if kept.len() == 0 [ _Not shown by the record._ ] else [ #for s in kept [ #s.text ] ]
  ]
]

#let cut = t.letter.filter(s => s.gap)
#if cut.len() > 0 [
  == Removed for lack of evidence
  #for s in cut [ - #s.text ]
]
```

The templates use no Typst package, read no file, and are the only place the words "Experience"
and "Questions" live. `hyphenate: false` is required by R8.

- [ ] **Step 2: Write the failing tests (Review Focus 4)**

`internal/adapters/outbound/tools/render_test.go`:

```go
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
	expect := map[string]string{
		"cv.typ":     `["Not shown by the record", "sails ships"]`,
		"letter.typ": `["Not shown by the record", "Removed for lack of evidence", "I sail ships."]`,
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
```

The `writeTemplate` data strings are hand-escaped JSON; if that grows unreadable, build them with
`json.Marshal` instead.

- [ ] **Step 3: Implement**

`internal/adapters/outbound/tools/render.go`:

```go
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
```

- [ ] **Step 4: Run, re-measure R8 with tabula, commit**

Run: `go test ./internal/adapters/outbound/tools/ -run 'Render|Templates' -race -v` — typst is
installed, so these must run, not skip; paste the output.

R8 re-measurement: `TestRenderKeepsDataAsTextAndVerifiesIt`'s first item is long enough to wrap.
If tabula's text differs from `pdftotext`'s in a way the join does not cover (for example tabula
drops the line break without a space, or re-orders lines), fix the normalisation in `verifyText`,
record exactly what tabula produced in the task report, and keep the test.

Mutation check: remove the `strings.ReplaceAll(text, "-\n", "-")` join and give the long item a
real hyphenated word placed to wrap (e.g. repeat "north-light" until it wraps); confirm the test
fails, then restore. If tabula never breaks at the hyphen, say so and drop the join.

```bash
git add internal/adapters/outbound/tools/render.go internal/adapters/outbound/tools/render_test.go packs/tailor
git commit -m "Render checked data through a Typst template and verify the PDF says what the record says"
```

---

### Task 7: Config, the composition root, the vocabulary guard

**Files:**
- Modify: `internal/config/config.go`, `internal/config/layers.go`, `internal/config/config_test.go`
- Modify: `cmd/atlas/main.go`, `cmd/atlas/main_test.go`
- Modify: `internal/arch/vocabulary_test.go`

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces:
  - `config.RenderConfig{TypstPath string; Timeout time.Duration; MaxBytes int64}` on
    `Config.Render`. Defaults: `TypstPath ""` (rendering off), `Timeout 60s`,
    `MaxBytes 20 MiB`. Env `ATLAS_RENDER_TYPST`, `ATLAS_RENDER_TIMEOUT`,
    `ATLAS_RENDER_MAX_BYTES`; flag `-render-typst`. Validated only when `TypstPath` is set.
  - `startRender(cfg config.Config) (ports.Tool, error)` in `main.go`, literal-free.

- [ ] **Step 1: Write the failing tests (Review Focus 5)**

`internal/config/config_test.go`, in the file's existing style:

```go
func TestRenderIsOffByDefaultAndValidatedWhenOn(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil || cfg.Render.TypstPath != "" {
		t.Fatalf("default render %+v, %v", cfg.Render, err)
	}
	t.Setenv("ATLAS_RENDER_TYPST", "typst")
	t.Setenv("ATLAS_RENDER_TIMEOUT", "0s")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("a zero render timeout was accepted while rendering is on")
	}
}
```

`cmd/atlas/main_test.go`:

```go
func TestRenderConfiguredButAbsentFailsAtStartup(t *testing.T) {
	cfg := config.Config{}
	cfg.Render.TypstPath = filepath.Join(t.TempDir(), "no-such-typst")
	cfg.Render.Timeout = time.Second
	cfg.Render.MaxBytes = 1 << 20
	_, err := startRender(cfg)
	if err == nil || !strings.Contains(err.Error(), "no-such-typst") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildRegistryRegistersTheTailoringTools(t *testing.T) {
	docs, index := testStore(t)
	r := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}))
	for _, name := range []string{"text.spans", "span.resolve", "citations.judge", "claims.settle"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}
```

If `buildRegistry` has gained parameters since this plan was written (Epic 8 adds a crawler),
pass them the way `TestBuildRegistryRegistersTheJudgeTool` does. Add `startRender` to the list in `TestCompositionPassesNoLiterals`.

Add `"applicant", "hiring manager"` to `forbidden` in `internal/arch/vocabulary_test.go`.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/config/ ./cmd/atlas/ ./internal/arch/ -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

`config.go`: add `Render RenderConfig` to `Config` and:

```go
// RenderConfig locates the typst binary documents are rendered with.
// Rendering is on only when TypstPath is set.
type RenderConfig struct {
	TypstPath string
	Timeout   time.Duration
	MaxBytes  int64
}
```

In `validate()`:

```go
	if c.Render.TypstPath != "" {
		if c.Render.Timeout <= 0 {
			return fmt.Errorf("config: render timeout must be positive, got %s", c.Render.Timeout)
		}
		if c.Render.MaxBytes <= 0 {
			return fmt.Errorf("config: render max bytes must be positive, got %d", c.Render.MaxBytes)
		}
	}
```

`layers.go`: defaults `Render: RenderConfig{Timeout: 60 * time.Second, MaxBytes: 20 * 1024 * 1024}`;
env blocks in the file's existing style for the three variables; `fs.StringVar(&c.Render.TypstPath,
"render-typst", c.Render.TypstPath, "typst binary to render documents with; empty disables rendering")`.

`main.go`: in `buildRegistry`, add

```go
		tools.NewTextSpans(cfg.Pack.FileMaxBytes),
		tools.NewSpanResolve(),
		tools.NewCitationsJudge(judge, docs, index),
		tools.NewClaimsSettle(),
```

and:

```go
// startRender builds render.run over the configured typst binary. A binary
// that is configured but not installed fails here, before any pack runs.
func startRender(cfg config.Config) (ports.Tool, error) {
	conv, err := typstconv.New(typstconv.Config{
		Bin:      cfg.Render.TypstPath,
		Timeout:  cfg.Render.Timeout,
		MaxBytes: cfg.Render.MaxBytes,
	})
	if err != nil {
		return nil, err
	}
	return tools.NewRender(conv, cfg.Render.MaxBytes), nil
}
```

In `run()`, after `buildRegistry` and before the agent starts (so an agent offered `render.run`
through `Agent.Tools` finds it):

```go
	if cfg.Render.TypstPath != "" {
		render, err := startRender(cfg)
		if err != nil {
			return err
		}
		registry = registry.With(render)
	}
```

- [ ] **Step 4: Run and commit**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`. Expected: all PASS.
Run `go run ./cmd/atlas -pack packs/hn-summary.yaml` and confirm it is unaffected (it needs Ollama).

```bash
git commit -am "Register the tailoring tools, and render only when a typst binary is configured"
```

---

### Task 8: The regression test, the pack, and job-hunt

**Files:**
- Create: `internal/adapters/outbound/tools/tailor_test.go`
- Create: `packs/tailor.yaml`
- Modify: `packs/job-hunt.yaml`

**Interfaces:**
- Consumes: every tool above, and `extract.run`, `quote.ground`, `file.read`, `docs.put`.
- Produces: `packs/tailor.yaml` with vars `store_root`, `evidence_dir` (optional), `posting`,
  `questions` (optional), `subject_id`, `out_dir`, `templates` (default `packs/tailor`),
  `relevance_threshold` (default `"0.6"`), `min_chars` (default `"25"`).

- [ ] **Step 1: The regression test this epic exists for**

`internal/adapters/outbound/tools/tailor_test.go` chains `text.spans` → `span.resolve` →
`quote.ground` → `citations.judge` → `claims.settle` with a canned extraction, exactly as the
pack does, and asserts nothing unsupported survives:

```go
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
```

`relevanceJudge` and `store` come from Task 3's test file. The judge says yes only to evidence
mentioning "ship", so span 1 ("Refitted the lamp lens") is grounded but irrelevant to "holds a
harbour pilot licence" and to "Yes, I am licensed."; span 42 and 99 do not exist; the tall-ship
sentence cites nothing.

Run: `go test ./internal/adapters/outbound/tools/ -run NothingUnsupported -race -v`. Expected: PASS.

Mutation check: in `app.Settle`, drop the relevance condition; confirm this test fails naming
"pilot licence". Revert.

- [ ] **Step 2: `packs/tailor.yaml`**

```yaml
# Tailors application documents to one posting from the record. Every
# statement about the candidate cites spans of the candidate's own text;
# anything uncited or unsupported is listed as a gap, never written as fact.
#   ATLAS_RENDER_TYPST=typst atlas -pack packs/tailor.yaml \
#     -var store_root="$HOME/.atlas/workspace" -var subject_id=acme-backend \
#     -var out_dir="$HOME/applications/acme" -var posting="$(cat posting.txt)"
name: tailor
vars:
  store_root: ""
  evidence_dir: ""
  posting: ""
  questions: ""
  subject_id: ""
  out_dir: ""
  templates: "packs/tailor"
  relevance_threshold: "0.6"
  min_chars: "25"

steps:
  - id: spans
    tool: text.spans
    with:
      paths: |
        {{ .vars.store_root }}/profile/source.txt
        {{ if .vars.evidence_dir }}{{ .vars.evidence_dir }}{{ end }}
      min_chars: "{{ .vars.min_chars }}"

  - id: history
    tool: file.read
    with:
      path: "{{ .vars.store_root }}/profile/history.json"

  - id: requirements
    tool: extract.run
    with:
      text: |
        Task: list each requirement the posting states. For each, cite the numbers of the
        candidate's evidence spans that directly show the candidate meets it. Cite nothing
        if no span shows it. Never guess.

        Candidate's evidence spans:
        {{ .steps.spans.listing }}
        Posting:
        {{ .vars.posting }}
      schema: |
        {"type":"object","required":["requirements"],"additionalProperties":false,
         "properties":{"requirements":{"type":"array","items":{"type":"object",
           "required":["text","spans"],"additionalProperties":false,
           "properties":{"text":{"type":"string"},"spans":{"type":"array","items":{"type":"integer"}}}}}}}

  - id: bullets
    tool: extract.run
    with:
      text: |
        Task: choose at most 8 of the candidate's evidence spans that best fit this posting,
        most relevant first. Cite each by its number only.

        Candidate's evidence spans:
        {{ .steps.spans.listing }}
        Posting:
        {{ .vars.posting }}
      schema: |
        {"type":"object","required":["bullets"],"additionalProperties":false,
         "properties":{"bullets":{"type":"array","maxItems":8,"items":{"type":"object",
           "required":["spans"],"additionalProperties":false,
           "properties":{"spans":{"type":"array","minItems":1,"maxItems":1,"items":{"type":"integer"}}}}}}}

  - id: letter
    tool: extract.run
    with:
      text: |
        Task: write a short letter of application of about six sentences, and answer each of
        the posting's questions. Every sentence about the candidate must cite the numbers of
        the evidence spans that support it. Do not write a sentence the spans do not support.

        Candidate's evidence spans:
        {{ .steps.spans.listing }}
        Posting:
        {{ .vars.posting }}
        Questions:
        {{ .vars.questions }}
      schema: |
        {"type":"object","required":["letter","answers"],"additionalProperties":false,
         "properties":{
          "letter":{"type":"array","items":{"type":"object","required":["text","spans"],"additionalProperties":false,
            "properties":{"text":{"type":"string"},"spans":{"type":"array","items":{"type":"integer"}}}}},
          "answers":{"type":"array","items":{"type":"object","required":["question","sentences"],"additionalProperties":false,
            "properties":{"question":{"type":"string"},"sentences":{"type":"array","items":{"type":"object",
              "required":["text","spans"],"additionalProperties":false,
              "properties":{"text":{"type":"string"},"spans":{"type":"array","items":{"type":"integer"}}}}}}}}}}

  - id: resolve
    tool: span.resolve
    with:
      fields: |
        {"requirements": {{ json .steps.requirements.fields.requirements }},
         "bullets": {{ json .steps.bullets.fields.bullets }},
         "letter": {{ json .steps.letter.fields.letter }},
         "answers": {{ json .steps.letter.fields.answers }}}
      spans: "{{ json .steps.spans.spans }}"

  - id: ground
    tool: quote.ground
    with:
      fields: "{{ json .steps.resolve.fields }}"
      source: "{{ .steps.spans.source }}"

  - id: judge
    tool: citations.judge
    with:
      fields: "{{ json .steps.ground.fields }}"
      threshold: "{{ .vars.relevance_threshold }}"
      subject_id: "{{ .vars.subject_id }}"

  - id: settle
    tool: claims.settle
    with:
      fields: "{{ json .steps.judge.fields }}"

  - id: save
    tool: docs.put
    with:
      path: "applications/{{ .vars.subject_id }}/tailored.json"
      body: "{{ json .steps.settle.fields }}"
      kind: tailored
      expect: json
      message: "Tailor for {{ .vars.subject_id }}"
      fields: |
        gaps: "{{ .steps.settle.gaps }}"

  - id: cv
    tool: render.run
    with:
      template: "{{ .vars.templates }}/cv.typ"
      data: '{"history": {{ .steps.history.body }}, "tailored": {{ json .steps.settle.fields }}}'
      output: "{{ .vars.out_dir }}/cv.pdf"
      expect: "{{ json .steps.settle.kept.bullets }}"

  - id: letter_pdf
    tool: render.run
    with:
      template: "{{ .vars.templates }}/letter.typ"
      data: '{"history": {{ .steps.history.body }}, "tailored": {{ json .steps.settle.fields }}}'
      output: "{{ .vars.out_dir }}/letter.pdf"
      expect: "{{ json .steps.settle.kept.letter }}"
```

`app.Settle` puts every top-level key in `kept`, empty when nothing survived (Task 4), so
`.steps.settle.kept.bullets` exists under `missingkey=error` even when every bullet is a gap. A
`docs.put` field value is YAML; keep `gaps` quoted as shown.

- [ ] **Step 3: Replace job-hunt's inventing step**

In `packs/job-hunt.yaml`: remove the `profile` var and the `documents` step. Add vars
`store_root`, `evidence_dir: ""`, `out_dir`, `templates: "packs/tailor"`,
`relevance_threshold: "0.6"`, `min_chars: "25"`, and append the steps of `packs/tailor.yaml`
from `spans` to `letter_pdf`, with `.vars.posting` replaced by
`Role: {{ .steps.role.title }} at {{ .steps.role.company }}\n\n{{ .steps.role.description_text }}`
and `.vars.subject_id` by `{{ .steps.role.id }}`. The `judged` and `verdict` steps used the
removed `profile` var: replace `Candidate: {{ .vars.profile }}` in both with
`Candidate: {{ .steps.spans.source }}`, which means `spans` and `history` move before `judged`.

- [ ] **Step 4: Run and commit**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`; then
`go run ./cmd/atlas -pack packs/tailor.yaml` with no vars must fail at `spans` naming the missing
`profile/source.txt` path (Review Focus 1), and paste that output.

```bash
git add internal/adapters/outbound/tools/tailor_test.go packs/tailor.yaml packs/job-hunt.yaml internal/core/app
git commit -m "Tailor from cited spans, and replace the step that invented experience"
```

---

### Task 9: The live run and the note

**Files:**
- Create: `docs/notes/2026-09-27-epic-9.md`

- [ ] **Step 1: A fabricated record**

In the scratchpad (never the repo), write a fabricated two-employer profile source text — a made-up
person, made-up companies, eight to twelve bullets — and a sample posting that asks for three
things the text supports and three it does not. Ingest it with `packs/profile-ingest.yaml` into a
throwaway store (it takes a PDF or DOCX: produce one with `typst compile` from a plain `.typ`
file).

- [ ] **Step 2: Run it**

```bash
ATLAS_RENDER_TYPST=typst go run ./cmd/atlas -pack packs/tailor.yaml \
  -store-root <scratch>/store -store-index-path <scratch>/index.db -store-history-path <scratch>/hist.duckdb \
  -var store_root=<scratch>/store -var subject_id=demo -var out_dir=<scratch>/out \
  -var posting="$(cat <scratch>/posting.txt)"
pdftotext <scratch>/out/cv.pdf - ; pdftotext <scratch>/out/letter.pdf -
```

If Ollama returns a CUDA error, retry once and record it; if it repeats, stop and report.

- [ ] **Step 3: Run the job-hunt documents failure again, for contrast**

Run the same posting through the old `documents` prompt (git show the previous `packs/job-hunt.yaml`
step into a scratch pack) and count invented employers, dates, certifications and posting-lifted
skills, as the spec did. The new pack's output must contain none.

- [ ] **Step 4: The note**

`docs/notes/2026-09-27-epic-9.md`, in the shape of earlier notes (what the pattern was, what
surprised you, what you would do differently, what you still do not understand). It must contain:
the fabricated run's rendered text in full; counts of requirements, gaps, kept bullets, kept and
removed letter sentences, and judge calls; the old step's invention counts on the same posting;
whether R10's fixed extraction message suppressed the letter; tabula's behaviour for R8; and the
limits the spec states (overstatement, headings from history).

- [ ] **Step 5: Commit**

```bash
git add docs/notes/2026-09-27-epic-9.md
git commit -m "Tailor a fabricated record end to end, and record what it did and did not catch"
```

## Deliberately not in this increment

As the spec's table, unchanged.

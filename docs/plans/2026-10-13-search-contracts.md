# Search contracts — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `internal/core/search`, the generic contracts package (field declarations,
criteria, the enriched document envelope, the generated enrichment schema, and `Match`), plus
the job pack's declaration and worked examples in `packs/job-search/`, so Sessions A to D can
build against them in parallel.

**Architecture:** One pure core package with no I/O. A pack declares its fields in JSON;
`ParseFields` loads them, `ParseCriteria` validates criteria against them, `Fields.Schema`
generates the JSON Schema an enricher hands the model, and `Match` ranks enrichments against
criteria. The package's tests use a toy bakery domain; one test globs `packs/*/fields.json`
and checks every pack's examples, naming no pack.

**Tech Stack:** Go 1.27, standard library only in non-test code;
`github.com/google/jsonschema-go` v0.4.3 in tests.

**Spec:** `docs/specs/2026-10-12-search-contracts-design.md`

## Global Constraints

- Non-test code under `internal/core/search` imports only the standard library (`internal/arch` enforces adapter and driver imports).
- No use-case vocabulary in any `.go` file: `TestGoTreeIsFreeOfUseCaseVocabulary` must pass. The words `posting`, `salary`, `employer`, `hiring`, `career`, `applicant`, `vacanc`, and whole-word `cv` and `jobs` never appear under `internal/`. Job vocabulary lives only in `packs/job-search/`.
- `packs/*/testdata/*.json` is reserved for render-template fixtures (`TestPackTemplateFixturesRender`). The job pack's search examples go in `packs/job-search/examples/`.
- Criteria and field declarations are decoded strictly: an unknown key is an error.
- `Match` is pure: no I/O, no model, no clock; identical inputs marshal to identical bytes.
- Every rejection names every problem, joined with `errors.Join`.
- `cp` and `rm` are aliased to interactive mode in this shell; use `command cp -f` and `command rm -f`.

## Review Focus

1. A misspelt criteria key such as `"partail"` must be rejected, not silently dropped (Task 3: the `misspelt key` case).
2. A misspelt declaration key such as `"enun"` must be rejected (Task 1: the `misspelt key` case).
3. A display path that is a partial segment, `"pri"` for the `price.*` group, must be rejected (Task 3: the `partial segment display` case).
4. An `in` or `contains_any` with an empty list must be rejected, since it could never be met (Task 3: the `empty in list` case).
5. An enrichment whose `values` is `null` must still give a row with every criterion `unknown` (Task 4: `TestASubjectThatSaysNothingStillHasARow` runs `{}` and `null`).

---

### Task 1: Field declarations

**Files:**
- Create: `internal/core/search/fields.go`
- Create: `internal/core/search/testdata/bakery-fields.json`
- Test: `internal/core/search/helpers_test.go`, `internal/core/search/fields_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Fields struct { Version int; ByPath map[string]Field; Aliases map[string]string }`, `type Field struct { Kind string; Enum []string; Pattern string; Canonical bool; Min, Max *int }`, `func ParseFields([]byte) (Fields, error)`, `func (Fields) Normalize(string) string`, unexported `ops map[string][]string`, `(Fields) allows(Field, string) bool`, `(Fields) displayable(string) bool`, `decodeStrict([]byte, any) error`. Test helpers `bakery(t) search.Fields` and `mustRead(t, path) []byte`.

- [ ] **Step 1: Write the toy declaration and the failing tests**

`internal/core/search/testdata/bakery-fields.json`:

```json
{
  "version": 1,
  "fields": {
    "loaf.grain": {"kind": "scalar", "enum": ["wheat", "rye", "spelt"]},
    "loaf.vegan": {"kind": "bool"},
    "loaf.toppings": {"kind": "list", "canonical": true},
    "price.amount": {"kind": "number"},
    "price.currency": {"kind": "scalar", "pattern": "^[A-Z]{3}$"},
    "open.hours": {"kind": "range", "min": 0, "max": 24},
    "notes": {"kind": "text"},
    "allergens": {"kind": "texts"}
  },
  "aliases": {"sesame seeds": "sesame", "poppy seeds": "poppy"}
}
```

`internal/core/search/helpers_test.go`:

```go
package search_test

import (
	"os"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

// bakery loads the toy declaration the package's tests search over.
func bakery(t *testing.T) search.Fields {
	t.Helper()
	f, err := search.ParseFields(mustRead(t, "testdata/bakery-fields.json"))
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	return f
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
```

`internal/core/search/fields_test.go`:

```go
package search_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

func TestParseFieldsNamesEveryProblem(t *testing.T) {
	_, err := search.ParseFields([]byte(`{"version": 1, "fields": {
	  "loaf.crust": {"kind": "crispy"},
	  "loaf.grain": {"kind": "scalar", "enum": ["rye"], "pattern": "^r"},
	  "open.hours": {"kind": "range", "min": 0},
	  "price": {"kind": "number"},
	  "price.amount": {"kind": "number"}}}`))
	if err == nil {
		t.Fatal("ParseFields accepted an invalid declaration")
	}
	for _, want := range []string{
		`field "loaf.crust": kind "crispy" is not one of`,
		`field "loaf.grain": has both enum and pattern`,
		`field "open.hours": a range needs min and max`,
		`field "price": prefixes "price.amount"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q; got:\n%v", want, err)
		}
	}
}

func TestParseFieldsRejectsMalformedDeclarations(t *testing.T) {
	cases := map[string]string{
		"bad pattern":    `{"version": 1, "fields": {"a": {"kind": "scalar", "pattern": "("}}}`,
		"uppercase path": `{"version": 1, "fields": {"Loaf.grain": {"kind": "scalar"}}}`,
		"wrong version":  `{"version": 2, "fields": {}}`,
		"misspelt key":   `{"version": 1, "fields": {"a": {"kind": "scalar", "enun": ["x"]}}}`,
	}
	for name, doc := range cases {
		if _, err := search.ParseFields([]byte(doc)); err == nil {
			t.Errorf("%s: ParseFields accepted it", name)
		}
	}
}

func TestNormalizeMapsAliasesToCanonicalNames(t *testing.T) {
	f := bakery(t)
	for in, want := range map[string]string{" Sesame Seeds ": "sesame", "Poppy seeds": "poppy", "Oats": "oats"} {
		if got := f.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/search/`
Expected: build failure, `undefined: search.ParseFields`.

- [ ] **Step 3: Implement**

`internal/core/search/fields.go`:

```go
package search

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Fields is a pack's declaration of the fields its enricher fills.
type Fields struct {
	Version int               `json:"version"`
	ByPath  map[string]Field  `json:"fields"`
	Aliases map[string]string `json:"aliases"`
}

// Field declares one dotted path: its kind and the strings it allows.
type Field struct {
	Kind      string   `json:"kind"`
	Enum      []string `json:"enum,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Canonical bool     `json:"canonical,omitempty"`
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`

	re *regexp.Regexp
}

// ops lists the operators each kind accepts. A kind with none is display only.
var ops = map[string][]string{
	"scalar": {"==", "in"},
	"bool":   {"=="},
	"number": {"==", ">=", "<="},
	"list":   {"contains_any"},
	"range":  {"overlaps"},
	"text":   nil,
	"texts":  nil,
}

var fieldPath = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)

// ParseFields decodes a field declaration and reports every problem in it.
func ParseFields(data []byte) (Fields, error) {
	var f Fields
	if err := decodeStrict(data, &f); err != nil {
		return Fields{}, fmt.Errorf("fields: %w", err)
	}
	var errs []error
	if f.Version != 1 {
		errs = append(errs, fmt.Errorf("fields: version %d is not supported; want 1", f.Version))
	}
	paths := slices.Sorted(maps.Keys(f.ByPath))
	for i, path := range paths {
		fd, err := parseField(path, f.ByPath[path])
		errs = append(errs, err)
		f.ByPath[path] = fd
		if i+1 < len(paths) && strings.HasPrefix(paths[i+1], path+".") {
			errs = append(errs, fmt.Errorf("field %q: prefixes %q", path, paths[i+1]))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Fields{}, err
	}
	return f, nil
}

func parseField(path string, fd Field) (Field, error) {
	var errs []error
	at := fmt.Sprintf("field %q", path)
	if !fieldPath.MatchString(path) {
		errs = append(errs, fmt.Errorf("%s: path must match %s", at, fieldPath))
	}
	if _, ok := ops[fd.Kind]; !ok {
		errs = append(errs, fmt.Errorf("%s: kind %q is not one of scalar, bool, number, list, range, text, texts", at, fd.Kind))
	}
	if len(fd.Enum) > 0 && fd.Pattern != "" {
		errs = append(errs, fmt.Errorf("%s: has both enum and pattern", at))
	}
	if fd.Pattern != "" {
		re, err := regexp.Compile(fd.Pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: pattern: %w", at, err))
		}
		fd.re = re
	}
	if fd.Kind == "range" && (fd.Min == nil || fd.Max == nil) {
		errs = append(errs, fmt.Errorf("%s: a range needs min and max", at))
	}
	return fd, errors.Join(errs...)
}

// Normalize returns the canonical form of a string: trimmed, lowercased and
// mapped through the declaration's aliases.
func (f Fields) Normalize(s string) string {
	n := strings.ToLower(strings.TrimSpace(s))
	if canonical, ok := f.Aliases[n]; ok {
		return canonical
	}
	return n
}

// allows reports whether s is in fd's vocabulary.
func (f Fields) allows(fd Field, s string) bool {
	switch {
	case len(fd.Enum) > 0:
		return slices.Contains(fd.Enum, s)
	case fd.re != nil:
		return fd.re.MatchString(s)
	case fd.Canonical:
		return s != "" && f.Normalize(s) == s
	}
	return true
}

// displayable reports whether path is a declared field or a prefix of one.
func (f Fields) displayable(path string) bool {
	if _, ok := f.ByPath[path]; ok {
		return true
	}
	for p := range f.ByPath {
		if strings.HasPrefix(p, path+".") {
			return true
		}
	}
	return false
}

// decodeStrict decodes data into v and rejects keys v does not declare.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/search/ ./internal/arch/`
Expected: both `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/search/fields.go internal/core/search/testdata/bakery-fields.json internal/core/search/helpers_test.go internal/core/search/fields_test.go
git commit -m "search: load a pack's field declaration"
```

---

### Task 2: Enriched document envelope and the generated schema

**Files:**
- Create: `internal/core/search/enrichment.go`, `internal/core/search/fields_schema.go`, `internal/core/search/schema.go`, `internal/core/search/schema/enrichment.json`
- Modify: `go.mod` (`github.com/google/jsonschema-go v0.4.3` moves from indirect to direct)
- Test: `internal/core/search/schema_test.go`

**Interfaces:**
- Consumes: `Fields`, `Field`, `bakery`, `mustRead` (Task 1).
- Produces: `type Enrichment struct { Version int; SubjectID string; ContentHash string; Enricher Enricher; EnrichedAt time.Time; Values map[string]any; Evidence map[string]string }`, `type Enricher struct { Engine, Model string; PromptVersion int; Error string }`, `func (*Enrichment) Value(path string) any`, `func (Fields) Schema() []byte`, `var Schemas embed.FS` holding `schema/*.json`. Test helpers `resolve(t, name, data) *jsonschema.Resolved`, `envelope(t, name) *jsonschema.Resolved`, `instance(t, data) any`.

`schema/criteria.json` and `schema/result.json` arrive in Tasks 3 and 4; the `schema/*.json` embed pattern already matches them.

- [ ] **Step 1: Write the failing tests**

`internal/core/search/schema_test.go`:

```go
package search_test

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/tunedev/atlas/internal/core/search"
)

func resolve(t *testing.T, name string, data []byte) *jsonschema.Resolved {
	t.Helper()
	var s jsonschema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	rs, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("%s: resolve: %v", name, err)
	}
	return rs
}

// envelope resolves one of the embedded schemas.
func envelope(t *testing.T, name string) *jsonschema.Resolved {
	t.Helper()
	data, err := search.Schemas.ReadFile("schema/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return resolve(t, name, data)
}

func instance(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

const goodLoaf = `{
  "loaf": {"grain": "rye", "vegan": true, "toppings": ["sesame"]},
  "price": {"amount": 4.5, "currency": "EUR"},
  "open": {"hours": [7, 18]},
  "notes": "baked at dawn",
  "allergens": ["sesame"]
}`

func TestTheGeneratedSchemaAcceptsValidValues(t *testing.T) {
	rs := resolve(t, "generated", bakery(t).Schema())
	for name, values := range map[string]string{
		"every field stated": goodLoaf,
		"every field null": `{"loaf": {"grain": null, "vegan": null, "toppings": null},
		  "price": {"amount": null, "currency": null}, "open": {"hours": null},
		  "notes": null, "allergens": null}`,
	} {
		if err := rs.Validate(instance(t, []byte(values))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheGeneratedSchemaRejectsValuesOutsideTheDeclaration(t *testing.T) {
	rs := resolve(t, "generated", bakery(t).Schema())
	for name, edit := range map[string]func(map[string]any){
		"enum":          func(v map[string]any) { at(v, "loaf")["grain"] = "barley" },
		"pattern":       func(v map[string]any) { at(v, "price")["currency"] = "euro" },
		"canonical":     func(v map[string]any) { at(v, "loaf")["toppings"] = []any{"Sesame"} },
		"range bound":   func(v map[string]any) { at(v, "open")["hours"] = []any{7, 30} },
		"empty list":    func(v map[string]any) { v["allergens"] = []any{} },
		"wrong type":    func(v map[string]any) { at(v, "loaf")["vegan"] = "yes" },
		"missing field": func(v map[string]any) { delete(at(v, "price"), "amount") },
		"extra field":   func(v map[string]any) { at(v, "loaf")["crust"] = "crisp" },
	} {
		v := instance(t, []byte(goodLoaf)).(map[string]any)
		edit(v)
		if err := rs.Validate(v); err == nil {
			t.Errorf("%s: the generated schema accepted it", name)
		}
	}
}

func at(v map[string]any, key string) map[string]any { return v[key].(map[string]any) }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/search/`
Expected: build failure, `undefined: search.Schemas` and `bakery(t).Schema undefined`. If `go` reports `jsonschema-go` missing from `go.mod`, run `go mod tidy`; it moves the existing indirect requirement to direct and adds nothing new.

- [ ] **Step 3: Implement**

`internal/core/search/enrichment.go`:

```go
package search

import (
	"strings"
	"time"
)

// Enrichment is what a model read out of one subject. Values nests the
// pack's declared fields by path segment; a null means the subject does not
// say. Evidence maps a field path to a verbatim quote.
type Enrichment struct {
	Version     int               `json:"version"`
	SubjectID   string            `json:"subject_id"`
	ContentHash string            `json:"content_hash"`
	Enricher    Enricher          `json:"enricher"`
	EnrichedAt  time.Time         `json:"enriched_at"`
	Values      map[string]any    `json:"values"`
	Evidence    map[string]string `json:"evidence"`
}

type Enricher struct {
	Engine        string `json:"engine"`
	Model         string `json:"model"`
	PromptVersion int    `json:"prompt_version"`
	Error         string `json:"error,omitempty"`
}

// Value returns the value at a dotted path, or nil when it is absent or null.
func (e *Enrichment) Value(path string) any {
	var v any = e.Values
	for seg := range strings.SplitSeq(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[seg]
	}
	return v
}
```

`internal/core/search/fields_schema.go`:

```go
package search

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// Schema returns the JSON Schema of an enrichment's values: every declared
// field nested by path segment, required and nullable, and nothing else.
func (f Fields) Schema() []byte {
	root := map[string]any{}
	for path, fd := range f.ByPath {
		node := root
		segs := strings.Split(path, ".")
		for _, seg := range segs[:len(segs)-1] {
			child, ok := node[seg].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[seg] = child
			}
			node = child
		}
		node[segs[len(segs)-1]] = leafSchema(fd)
	}
	s := objectSchema(root)
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	// A tree of maps, slices, strings and numbers always marshals.
	b, _ := json.Marshal(s)
	return b
}

// objectSchema turns a tree whose leaves are schemas into object schemas.
// A leaf is told apart from a branch by its "type" or "enum" keyword.
func objectSchema(node map[string]any) map[string]any {
	props := map[string]any{}
	for name, v := range node {
		child := v.(map[string]any)
		if isLeaf(child) {
			props[name] = child
			continue
		}
		props[name] = objectSchema(child)
	}
	return map[string]any{
		"type":                 "object",
		"required":             slices.Sorted(maps.Keys(props)),
		"additionalProperties": false,
		"properties":           props,
	}
}

func isLeaf(node map[string]any) bool {
	_, typed := node["type"]
	_, enum := node["enum"]
	return typed || enum
}

func leafSchema(fd Field) map[string]any {
	switch fd.Kind {
	case "scalar":
		if len(fd.Enum) > 0 {
			return map[string]any{"enum": append(toAny(fd.Enum), nil)}
		}
		return nullable("string", word(fd))
	case "bool":
		return nullable("boolean", nil)
	case "number":
		return nullable("number", nil)
	case "list":
		return nullable("array", map[string]any{"minItems": 1, "items": item(fd)})
	case "range":
		return nullable("array", map[string]any{"minItems": 2, "maxItems": 2,
			"items": map[string]any{"type": "integer", "minimum": *fd.Min, "maximum": *fd.Max}})
	case "texts":
		return nullable("array", map[string]any{"minItems": 1, "items": map[string]any{"type": "string"}})
	}
	return nullable("string", nil)
}

func nullable(typ string, keywords map[string]any) map[string]any {
	s := map[string]any{"type": []string{typ, "null"}}
	maps.Copy(s, keywords)
	return s
}

func item(fd Field) map[string]any {
	s := map[string]any{"type": "string"}
	if len(fd.Enum) > 0 {
		s["enum"] = fd.Enum
	}
	maps.Copy(s, word(fd))
	return s
}

// word returns the pattern keyword a string must match: the declared
// pattern, or no uppercase for a canonical field.
func word(fd Field) map[string]any {
	switch {
	case fd.Pattern != "":
		return map[string]any{"pattern": fd.Pattern}
	case fd.Canonical:
		return map[string]any{"pattern": "^[^A-Z]+$"}
	}
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
```

`internal/core/search/schema.go`:

```go
package search

import "embed"

// Schemas holds the JSON Schemas of the three contracts: schema/criteria.json,
// schema/enrichment.json and schema/result.json.
//
//go:embed schema/*.json
var Schemas embed.FS
```

`internal/core/search/schema/enrichment.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Atlas enriched document, version 1",
  "type": "object",
  "required": ["version", "subject_id", "content_hash", "enricher", "enriched_at", "values", "evidence"],
  "additionalProperties": false,
  "properties": {
    "version": {"const": 1},
    "subject_id": {"type": "string", "minLength": 1},
    "content_hash": {"type": "string"},
    "enricher": {
      "type": "object",
      "required": ["engine", "model", "prompt_version"],
      "additionalProperties": false,
      "properties": {
        "engine": {"type": "string"},
        "model": {"type": "string"},
        "prompt_version": {"type": "integer", "minimum": 1},
        "error": {"type": "string"}
      }
    },
    "enriched_at": {"type": "string", "format": "date-time"},
    "values": {"type": "object"},
    "evidence": {"type": "object", "additionalProperties": {"type": "string"}}
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go mod tidy && git diff go.mod && go test ./internal/core/search/ ./internal/arch/`
Expected: the `go.mod` diff only moves `github.com/google/jsonschema-go v0.4.3` into the direct block; both packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add go.mod internal/core/search/enrichment.go internal/core/search/fields_schema.go internal/core/search/schema.go internal/core/search/schema/enrichment.json internal/core/search/schema_test.go
git commit -m "search: add the enrichment envelope and generate its values schema"
```

---

### Task 3: Criteria and validation

**Files:**
- Create: `internal/core/search/criteria.go`, `internal/core/search/validate.go`, `internal/core/search/schema/criteria.json`
- Test: `internal/core/search/criteria_test.go`

**Interfaces:**
- Consumes: `Fields`, `Field`, `ops`, `allows`, `displayable`, `decodeStrict`, `bakery` (Task 1).
- Produces: `type Criteria struct { Version int; Query string; Criteria []Criterion; Display []string; Hash string }`, `type Criterion struct { ID, Statement string; Must bool; Weight int; Check Condition; Partial, When *Condition }`, `type Condition struct { Field, Op string; Value any; Any, All []Condition }`, `func ParseCriteria([]byte, Fields) (Criteria, error)`. `Hash` is the hex sha256 of the bytes read.

- [ ] **Step 1: Write the failing tests**

`internal/core/search/criteria_test.go`:

```go
package search_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

func TestParseCriteriaNamesEveryProblem(t *testing.T) {
	_, err := search.ParseCriteria([]byte(`{
	  "version": 1,
	  "criteria": [
	    {"id": "a", "statement": "s", "weight": 1,
	     "check": {"field": "loaf.crust", "op": "==", "value": "x"}},
	    {"id": "b", "statement": "s", "weight": 1,
	     "check": {"field": "loaf.grain", "op": "==", "value": "barley"}},
	    {"id": "c", "statement": "s", "must": true,
	     "check": {"field": "loaf.toppings", "op": "contains_any", "value": ["sesame"]},
	     "partial": {"field": "loaf.toppings", "op": "contains_any", "value": ["poppy"]}},
	    {"id": "d", "statement": "s", "weight": 1,
	     "check": {"field": "notes", "op": "==", "value": "x"}}
	  ]
	}`), bakery(t))
	if err == nil {
		t.Fatal("ParseCriteria accepted an invalid set")
	}
	for _, want := range []string{
		`criterion "a" check: field "loaf.crust" is not a checkable declared field`,
		`criterion "b" check: loaf.grain: value barley is outside the vocabulary`,
		`criterion "c": a must-have cannot have partial`,
		`criterion "d" check: field "notes" is not a checkable declared field`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q; got:\n%v", want, err)
		}
	}
}

func TestParseCriteriaRejectsMalformedCriteria(t *testing.T) {
	cases := map[string]string{
		"repeated id": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye"}},
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`,
		"must and weight": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "must": true, "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`,
		"neither must nor weight": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`,
		"op does not fit the kind": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.toppings", "op": "==", "value": "sesame"}}]}`,
		"uncanonical value": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.toppings", "op": "contains_any", "value": ["sesame seeds"]}}]}`,
		"value outside the pattern": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "price.currency", "op": "==", "value": "euro"}}]}`,
		"range outside the bounds": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [20, 26]}}]}`,
		"backwards range": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [9, 6]}}]}`,
		"comparison and combinator together": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye",
		   "any": [{"field": "loaf.grain", "op": "==", "value": "spelt"}]}}]}`,
		"empty condition":         `{"version": 1, "criteria": [{"id": "a", "statement": "s", "weight": 1, "check": {}}]}`,
		"undeclared display":      `{"version": 1, "criteria": [], "display": ["crust"]}`,
		"partial segment display": `{"version": 1, "criteria": [], "display": ["pri"]}`,
		"empty in list": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "in", "value": []}}]}`,
		"misspelt key": `{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye"},
		   "partail": {"field": "loaf.grain", "op": "==", "value": "spelt"}}]}`,
		"wrong version": `{"version": 2, "criteria": []}`,
	}
	f := bakery(t)
	for name, doc := range cases {
		if _, err := search.ParseCriteria([]byte(doc), f); err == nil {
			t.Errorf("%s: ParseCriteria accepted it", name)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/search/`
Expected: build failure, `undefined: search.ParseCriteria`.

- [ ] **Step 3: Implement**

`internal/core/search/criteria.go`:

```go
package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Criteria is one search: must-haves that hide a subject when unmet, and
// weighted preferences that rank what is left.
type Criteria struct {
	Version  int         `json:"version"`
	Query    string      `json:"query"`
	Criteria []Criterion `json:"criteria"`
	Display  []string    `json:"display"`

	// Hash is the sha256 of the document ParseCriteria read.
	Hash string `json:"-"`
}

type Criterion struct {
	ID        string     `json:"id"`
	Statement string     `json:"statement"`
	Must      bool       `json:"must,omitempty"`
	Weight    int        `json:"weight,omitempty"`
	Check     Condition  `json:"check"`
	Partial   *Condition `json:"partial,omitempty"`
	When      *Condition `json:"when,omitempty"`
}

// Condition is a comparison (Field, Op, Value) or a combinator (Any or All).
type Condition struct {
	Field string      `json:"field,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value any         `json:"value,omitempty"`
	Any   []Condition `json:"any,omitempty"`
	All   []Condition `json:"all,omitempty"`
}

// ParseCriteria decodes a stored criteria document and validates it against
// a field declaration.
func ParseCriteria(data []byte, f Fields) (Criteria, error) {
	var c Criteria
	if err := decodeStrict(data, &c); err != nil {
		return Criteria{}, fmt.Errorf("criteria: %w", err)
	}
	if err := validate(c, f); err != nil {
		return Criteria{}, err
	}
	sum := sha256.Sum256(data)
	c.Hash = hex.EncodeToString(sum[:])
	return c, nil
}
```

`internal/core/search/validate.go`:

```go
package search

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
)

var criterionID = regexp.MustCompile(`^[a-z0-9-]+$`)

// validate reports every problem in a criteria set, joined into one error.
func validate(c Criteria, f Fields) error {
	var errs []error
	if c.Version != 1 {
		errs = append(errs, fmt.Errorf("version %d is not supported; want 1", c.Version))
	}
	seen := map[string]bool{}
	for _, cr := range c.Criteria {
		errs = append(errs, validateCriterion(f, cr, seen)...)
	}
	for _, path := range c.Display {
		if !f.displayable(path) {
			errs = append(errs, fmt.Errorf("display %q is not a declared field", path))
		}
	}
	return errors.Join(errs...)
}

func validateCriterion(f Fields, cr Criterion, seen map[string]bool) []error {
	var errs []error
	at := fmt.Sprintf("criterion %q", cr.ID)
	if !criterionID.MatchString(cr.ID) {
		errs = append(errs, fmt.Errorf("%s: id must match %s", at, criterionID))
	}
	if seen[cr.ID] {
		errs = append(errs, fmt.Errorf("%s: id repeats", at))
	}
	seen[cr.ID] = true
	if cr.Must == (cr.Weight != 0) {
		errs = append(errs, fmt.Errorf("%s: needs exactly one of must and weight", at))
	}
	if cr.Weight < 0 {
		errs = append(errs, fmt.Errorf("%s: weight must be positive", at))
	}
	if cr.Must && cr.Partial != nil {
		errs = append(errs, fmt.Errorf("%s: a must-have cannot have partial", at))
	}
	errs = append(errs, validateCondition(f, at+" check", cr.Check)...)
	if cr.Partial != nil {
		errs = append(errs, validateCondition(f, at+" partial", *cr.Partial)...)
	}
	if cr.When != nil {
		errs = append(errs, validateCondition(f, at+" when", *cr.When)...)
	}
	return errs
}

func validateCondition(f Fields, at string, c Condition) []error {
	comparison := c.Field != "" || c.Op != "" || c.Value != nil
	combinators := 0
	for _, children := range [][]Condition{c.Any, c.All} {
		if len(children) > 0 {
			combinators++
		}
	}
	switch {
	case comparison && combinators > 0, combinators > 1:
		return []error{fmt.Errorf("%s: a condition is one comparison, one any, or one all", at)}
	case combinators == 1:
		var errs []error
		for i, child := range slices.Concat(c.Any, c.All) {
			errs = append(errs, validateCondition(f, fmt.Sprintf("%s[%d]", at, i), child)...)
		}
		return errs
	case !comparison:
		return []error{fmt.Errorf("%s: empty condition", at)}
	}
	return validateComparison(f, at, c)
}

func validateComparison(f Fields, at string, c Condition) []error {
	fd, ok := f.ByPath[c.Field]
	if !ok || len(ops[fd.Kind]) == 0 {
		return []error{fmt.Errorf("%s: field %q is not a checkable declared field", at, c.Field)}
	}
	if !slices.Contains(ops[fd.Kind], c.Op) {
		return []error{fmt.Errorf("%s: op %q does not apply to %s; want one of %v", at, c.Op, c.Field, ops[fd.Kind])}
	}
	if err := f.validateValue(fd, c.Op, c.Value); err != nil {
		return []error{fmt.Errorf("%s: %s: %w", at, c.Field, err)}
	}
	return nil
}

func (f Fields) validateValue(fd Field, op string, v any) error {
	switch fd.Kind {
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("value %v is not a boolean", v)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("value %v is not a number", v)
		}
	case "scalar":
		if op == "==" {
			return f.validateWord(fd, v)
		}
		return f.validateWords(fd, v)
	case "list":
		return f.validateWords(fd, v)
	case "range":
		return validateRange(fd, v)
	}
	return nil
}

func (f Fields) validateWords(fd Field, v any) error {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return fmt.Errorf("value %v is not a non-empty list", v)
	}
	var errs []error
	for _, item := range items {
		errs = append(errs, f.validateWord(fd, item))
	}
	return errors.Join(errs...)
}

func (f Fields) validateWord(fd Field, v any) error {
	s, ok := v.(string)
	if !ok || !f.allows(fd, s) {
		return fmt.Errorf("value %v is outside the vocabulary", v)
	}
	return nil
}

func validateRange(fd Field, v any) error {
	items, ok := v.([]any)
	if !ok || len(items) != 2 {
		return fmt.Errorf("value %v is not a [min, max] pair", v)
	}
	lo, loOK := items[0].(float64)
	hi, hiOK := items[1].(float64)
	whole := loOK && hiOK && lo == math.Trunc(lo) && hi == math.Trunc(hi)
	if !whole || lo < float64(*fd.Min) || hi > float64(*fd.Max) || lo > hi {
		return fmt.Errorf("value %v is not an integer range within %d to %d", v, *fd.Min, *fd.Max)
	}
	return nil
}
```

`internal/core/search/schema/criteria.json` (for adapters and the Task 5 pack check; `ParseCriteria` does not read it):

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Atlas search criteria, version 1",
  "type": "object",
  "required": ["version", "criteria"],
  "additionalProperties": false,
  "properties": {
    "version": {"const": 1},
    "query": {"type": "string"},
    "criteria": {"type": "array", "items": {"$ref": "#/$defs/criterion"}},
    "display": {"type": "array", "items": {"type": "string"}}
  },
  "$defs": {
    "criterion": {
      "type": "object",
      "required": ["id", "statement", "check"],
      "additionalProperties": false,
      "properties": {
        "id": {"type": "string", "pattern": "^[a-z0-9-]+$"},
        "statement": {"type": "string"},
        "must": {"type": "boolean"},
        "weight": {"type": "integer", "minimum": 1},
        "check": {"$ref": "#/$defs/condition"},
        "partial": {"$ref": "#/$defs/condition"},
        "when": {"$ref": "#/$defs/condition"}
      }
    },
    "condition": {
      "oneOf": [
        {
          "type": "object",
          "required": ["field", "op", "value"],
          "additionalProperties": false,
          "properties": {
            "field": {"type": "string"},
            "op": {"enum": ["==", "in", "contains_any", ">=", "<=", "overlaps"]},
            "value": {}
          }
        },
        {
          "type": "object",
          "required": ["any"],
          "additionalProperties": false,
          "properties": {"any": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/condition"}}}
        },
        {
          "type": "object",
          "required": ["all"],
          "additionalProperties": false,
          "properties": {"all": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/condition"}}}
        }
      ]
    }
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/search/ ./internal/arch/`
Expected: both `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/search/criteria.go internal/core/search/validate.go internal/core/search/schema/criteria.json internal/core/search/criteria_test.go
git commit -m "search: parse and validate criteria against a field declaration"
```

---

### Task 4: Match

**Files:**
- Create: `internal/core/search/match.go`, `internal/core/search/schema/result.json`
- Test: `internal/core/search/match_test.go`

**Interfaces:**
- Consumes: `Criteria`, `Criterion`, `Condition`, `ParseCriteria` (Task 3); `Enrichment`, `Value` (Task 2); `bakery` (Task 1).
- Produces: `type Outcome string` with `Met`, `Unmet`, `Unknown`, `Partial`, `NA`; `type Result struct { Version int; CriteriaHash string; Checked, Hidden int; Rows []Row }`; `type Row struct { SubjectID string; Score, Known float64; MustUnknown []string; Results []CriterionResult; Display map[string]any }`; `type CriterionResult struct { ID string; Outcome Outcome; Evidence string }`; `func Match(Criteria, []Enrichment) Result`.

- [ ] **Step 1: Write the failing tests**

`internal/core/search/match_test.go`:

```go
package search_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

func criteria(t *testing.T, doc string) search.Criteria {
	t.Helper()
	c, err := search.ParseCriteria([]byte(doc), bakery(t))
	if err != nil {
		t.Fatalf("ParseCriteria: %v", err)
	}
	return c
}

// subject builds an enrichment; every field values leaves out is null.
func subject(t *testing.T, id, values string) search.Enrichment {
	t.Helper()
	e := search.Enrichment{SubjectID: id}
	if err := json.Unmarshal([]byte(values), &e.Values); err != nil {
		t.Fatalf("values: %v", err)
	}
	return e
}

func onlyRow(t *testing.T, res search.Result) search.Row {
	t.Helper()
	if len(res.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(res.Rows))
	}
	return res.Rows[0]
}

func outcome(row search.Row, id string) search.Outcome {
	for _, r := range row.Results {
		if r.ID == id {
			return r.Outcome
		}
	}
	return ""
}

func ids(res search.Result) []string {
	var out []string
	for _, row := range res.Rows {
		out = append(out, row.SubjectID)
	}
	return out
}

const loafCriteria = `{"version": 1, "criteria": [
  {"id": "toppings", "statement": "Sesame first", "weight": 2,
   "check": {"field": "loaf.toppings", "op": "contains_any", "value": ["sesame"]},
   "partial": {"field": "loaf.toppings", "op": "contains_any", "value": ["poppy"]}},
  {"id": "grain", "statement": "Rye", "weight": 2,
   "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`

const veganCriteria = `{"version": 1, "criteria": [
  {"id": "vegan", "statement": "Vegan", "must": true,
   "check": {"field": "loaf.vegan", "op": "==", "value": true}},
  {"id": "grain", "statement": "Rye", "weight": 1,
   "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`

func TestAnUnmetMustHaveHidesTheSubject(t *testing.T) {
	c := criteria(t, veganCriteria)
	res := search.Match(c, []search.Enrichment{
		subject(t, "kept", `{"loaf": {"vegan": true}}`),
		subject(t, "hidden", `{"loaf": {"vegan": false, "grain": "rye"}}`),
	})
	if res.Checked != 2 || res.Hidden != 1 {
		t.Errorf("checked, hidden = %d, %d; want 2, 1", res.Checked, res.Hidden)
	}
	if got := ids(res); !slices.Equal(got, []string{"kept"}) {
		t.Errorf("rows = %v, want [kept]", got)
	}
}

func TestAnUnknownMustHaveIsListedAfterFullPasses(t *testing.T) {
	c := criteria(t, veganCriteria)
	res := search.Match(c, []search.Enrichment{
		subject(t, "unknown", `{"loaf": {"grain": "rye"}}`),
		subject(t, "full-pass", `{"loaf": {"vegan": true, "grain": "wheat"}}`),
	})
	if got := ids(res); !slices.Equal(got, []string{"full-pass", "unknown"}) {
		t.Fatalf("order = %v, want the full pass first despite its lower score", got)
	}
	if got := res.Rows[1].MustUnknown; !slices.Equal(got, []string{"vegan"}) {
		t.Errorf("must_unknown = %v, want [vegan]", got)
	}
}

func TestPartialScoresHalfItsWeight(t *testing.T) {
	c := criteria(t, loafCriteria)
	e := subject(t, "s", `{"loaf": {"toppings": ["poppy"], "grain": "rye"}}`)

	row := onlyRow(t, search.Match(c, []search.Enrichment{e}))
	if got := outcome(row, "toppings"); got != search.Partial {
		t.Errorf("toppings outcome = %s, want partial", got)
	}
	if row.Score != 0.75 || row.Known != 1 {
		t.Errorf("score, known = %v, %v; want 0.75, 1", row.Score, row.Known)
	}
}

func TestAnUnmetWhenMakesTheCriterionNotApplicable(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [
	  {"id": "cheap", "statement": "Under 5 euro", "weight": 1,
	   "when": {"field": "price.currency", "op": "==", "value": "EUR"},
	   "check": {"field": "price.amount", "op": "<=", "value": 5}},
	  {"id": "grain", "statement": "Rye", "weight": 1,
	   "check": {"field": "loaf.grain", "op": "==", "value": "rye"}}]}`)
	e := subject(t, "s", `{"price": {"currency": "GBP", "amount": 9}, "loaf": {"grain": "rye"}}`)

	row := onlyRow(t, search.Match(c, []search.Enrichment{e}))
	if got := outcome(row, "cheap"); got != search.NA {
		t.Errorf("cheap outcome = %s, want n/a", got)
	}
	if row.Score != 1 || row.Known != 1 {
		t.Errorf("score, known = %v, %v; want 1, 1 with cheap left out", row.Score, row.Known)
	}
}

func TestAnUnknownWhenStillChecksTheCriterion(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [
	  {"id": "cheap", "statement": "Under 5 euro", "weight": 1,
	   "when": {"field": "price.currency", "op": "==", "value": "EUR"},
	   "check": {"field": "price.amount", "op": "<=", "value": 5}}]}`)
	e := subject(t, "s", `{"price": {"amount": 4}}`)

	if got := outcome(onlyRow(t, search.Match(c, []search.Enrichment{e})), "cheap"); got != search.Met {
		t.Errorf("cheap outcome = %s, want met", got)
	}
}

func TestASubjectThatSaysNothingStillHasARow(t *testing.T) {
	c := criteria(t, loafCriteria)
	for _, values := range []string{`{}`, `null`} {
		row := onlyRow(t, search.Match(c, []search.Enrichment{subject(t, "s", values)}))
		if row.Score != 0 || row.Known != 0 {
			t.Errorf("values %s: score, known = %v, %v; want 0, 0", values, row.Score, row.Known)
		}
		for _, r := range row.Results {
			if r.Outcome != search.Unknown {
				t.Errorf("values %s: %s outcome = %s, want unknown", values, r.ID, r.Outcome)
			}
		}
	}
}

func TestAnyOverNullAndMetIsMetButAllIsUnknown(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [
	  {"id": "any", "statement": "s", "weight": 1, "check": {"any": [
	    {"field": "loaf.vegan", "op": "==", "value": true},
	    {"field": "loaf.grain", "op": "==", "value": "rye"}]}},
	  {"id": "all", "statement": "s", "weight": 1, "check": {"all": [
	    {"field": "loaf.vegan", "op": "==", "value": true},
	    {"field": "loaf.grain", "op": "==", "value": "rye"}]}}]}`)
	e := subject(t, "s", `{"loaf": {"grain": "rye"}}`)
	e.Evidence = map[string]string{"loaf.grain": "dark rye"}

	row := onlyRow(t, search.Match(c, []search.Enrichment{e}))
	if got := outcome(row, "any"); got != search.Met {
		t.Errorf("any outcome = %s, want met", got)
	}
	if got := outcome(row, "all"); got != search.Unknown {
		t.Errorf("all outcome = %s, want unknown", got)
	}
	if got := row.Results[0].Evidence; got != "dark rye" {
		t.Errorf("any evidence = %q, want the met child's quote", got)
	}
}

func TestComparisonsAgainstStatedValues(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [
	  {"id": "floor", "statement": "s", "weight": 1, "check": {"field": "price.amount", "op": ">=", "value": 3}},
	  {"id": "cap", "statement": "s", "weight": 1, "check": {"field": "price.amount", "op": "<=", "value": 3}},
	  {"id": "grain", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "in", "value": ["rye", "spelt"]}},
	  {"id": "hours", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [6, 9]}}]}`)
	e := subject(t, "s", `{"price": {"amount": 4}, "loaf": {"grain": "wheat"}, "open": {"hours": [10, 18]}}`)

	row := onlyRow(t, search.Match(c, []search.Enrichment{e}))
	for id, want := range map[string]search.Outcome{"floor": search.Met, "cap": search.Unmet, "grain": search.Unmet, "hours": search.Unmet} {
		if got := outcome(row, id); got != want {
			t.Errorf("%s outcome = %s, want %s", id, got, want)
		}
	}
}

func TestAValueOfTheWrongTypeIsUnknown(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [
	  {"id": "grain", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye"}},
	  {"id": "toppings", "statement": "s", "weight": 1, "check": {"field": "loaf.toppings", "op": "contains_any", "value": ["sesame"]}},
	  {"id": "hours", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [6, 9]}}]}`)
	e := subject(t, "s", `{"loaf": {"grain": 3, "toppings": "sesame"}, "open": {"hours": [7]}}`)

	row := onlyRow(t, search.Match(c, []search.Enrichment{e}))
	for _, id := range []string{"grain", "toppings", "hours"} {
		if got := outcome(row, id); got != search.Unknown {
			t.Errorf("%s outcome = %s, want unknown", id, got)
		}
	}
}

func TestRowsRankByScoreThenKnownThenSubjectID(t *testing.T) {
	c := criteria(t, loafCriteria)
	res := search.Match(c, []search.Enrichment{
		subject(t, "b-tie", `{"loaf": {"toppings": ["sesame"]}}`),
		subject(t, "y-known", `{"loaf": {"toppings": ["sesame"], "grain": "wheat"}}`),
		subject(t, "a-tie", `{"loaf": {"toppings": ["sesame"]}}`),
		subject(t, "z-top", `{"loaf": {"toppings": ["sesame"], "grain": "rye"}}`),
	})
	want := []string{"z-top", "y-known", "a-tie", "b-tie"}
	if got := ids(res); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestDisplayCopiesAFieldOrAGroup(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [], "display": ["price", "loaf.grain"]}`)
	e := subject(t, "s", `{"price": {"amount": 4, "currency": null}, "loaf": {"grain": "rye"}}`)

	got, _ := json.Marshal(onlyRow(t, search.Match(c, []search.Enrichment{e})).Display)
	if want := `{"loaf.grain":"rye","price":{"amount":4,"currency":null}}`; string(got) != want {
		t.Errorf("display = %s, want %s", got, want)
	}
}

func TestMatchIsDeterministic(t *testing.T) {
	c := criteria(t, loafCriteria)
	es := []search.Enrichment{
		subject(t, "a", `{"loaf": {"toppings": ["poppy"], "grain": "rye"}}`),
		subject(t, "b", `{"loaf": {"toppings": ["sesame"]}}`),
		subject(t, "c", `{}`),
	}
	first, _ := json.Marshal(search.Match(c, es))
	again, _ := json.Marshal(search.Match(c, es))
	reversed, _ := json.Marshal(search.Match(c, []search.Enrichment{es[2], es[1], es[0]}))
	if !bytes.Equal(first, again) {
		t.Error("two runs on the same input differ")
	}
	if !bytes.Equal(first, reversed) {
		t.Error("reordering the input changes the result")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/search/`
Expected: build failure, `undefined: search.Match`.

- [ ] **Step 3: Implement**

`internal/core/search/match.go`:

```go
package search

import (
	"cmp"
	"slices"
)

type Outcome string

const (
	Met     Outcome = "met"
	Unmet   Outcome = "unmet"
	Unknown Outcome = "unknown"
	Partial Outcome = "partial"
	NA      Outcome = "n/a"
)

type Result struct {
	Version      int    `json:"version"`
	CriteriaHash string `json:"criteria_hash"`
	Checked      int    `json:"checked"`
	Hidden       int    `json:"hidden"`
	Rows         []Row  `json:"rows"`
}

type Row struct {
	SubjectID   string            `json:"subject_id"`
	Score       float64           `json:"score"`
	Known       float64           `json:"known"`
	MustUnknown []string          `json:"must_unknown,omitempty"`
	Results     []CriterionResult `json:"results"`
	Display     map[string]any    `json:"display"`
}

type CriterionResult struct {
	ID       string  `json:"id"`
	Outcome  Outcome `json:"outcome"`
	Evidence string  `json:"evidence,omitempty"`
}

// Match checks every enrichment against c and ranks what is not hidden.
// c must come from ParseCriteria.
func Match(c Criteria, es []Enrichment) Result {
	res := Result{Version: 1, CriteriaHash: c.Hash, Checked: len(es), Rows: []Row{}}
	for i := range es {
		row, hidden := matchOne(c, &es[i])
		if hidden {
			res.Hidden++
			continue
		}
		res.Rows = append(res.Rows, row)
	}
	slices.SortFunc(res.Rows, compareRows)
	return res
}

func matchOne(c Criteria, e *Enrichment) (Row, bool) {
	row := Row{SubjectID: e.SubjectID, Display: map[string]any{}}
	var total, earned, known float64
	for _, cr := range c.Criteria {
		r := judge(cr, e)
		row.Results = append(row.Results, r)
		if cr.Must {
			switch r.Outcome {
			case Unmet:
				return Row{}, true
			case Unknown:
				row.MustUnknown = append(row.MustUnknown, cr.ID)
			}
			continue
		}
		w := float64(cr.Weight)
		switch r.Outcome {
		case NA:
			continue
		case Met:
			earned += w
			known += w
		case Partial:
			earned += w / 2
			known += w
		case Unmet:
			known += w
		}
		total += w
	}
	if total > 0 {
		row.Score = earned / total
		row.Known = known / total
	}
	for _, path := range c.Display {
		row.Display[path] = e.Value(path)
	}
	return row, false
}

// judge applies the outcome rules: when, then check, then partial.
func judge(cr Criterion, e *Enrichment) CriterionResult {
	r := CriterionResult{ID: cr.ID}
	if cr.When != nil {
		if o, _ := eval(*cr.When, e); o == Unmet {
			r.Outcome = NA
			return r
		}
	}
	check, checkEvidence := eval(cr.Check, e)
	if check == Met {
		r.Outcome, r.Evidence = Met, checkEvidence
		return r
	}
	partial := Unmet
	if cr.Partial != nil {
		var partialEvidence string
		partial, partialEvidence = eval(*cr.Partial, e)
		if partial == Met {
			r.Outcome, r.Evidence = Partial, partialEvidence
			return r
		}
	}
	if check == Unknown || partial == Unknown {
		r.Outcome = Unknown
		return r
	}
	r.Outcome, r.Evidence = Unmet, checkEvidence
	return r
}

// eval returns a condition's outcome and the evidence of the comparison
// that decided it.
func eval(c Condition, e *Enrichment) (Outcome, string) {
	switch {
	case len(c.Any) > 0:
		return combine(c.Any, e, Met, Unmet)
	case len(c.All) > 0:
		return combine(c.All, e, Unmet, Met)
	}
	met, ok := compare(c.Op, e.Value(c.Field), c.Value)
	switch {
	case !ok:
		return Unknown, ""
	case met:
		return Met, e.Evidence[c.Field]
	}
	return Unmet, e.Evidence[c.Field]
}

// combine evaluates children: the first child with outcome decisive decides;
// otherwise any unknown child makes it unknown; otherwise it is fallback,
// evidenced by the first child.
func combine(children []Condition, e *Enrichment, decisive, fallback Outcome) (Outcome, string) {
	unknown := false
	var firstEvidence string
	for i, child := range children {
		o, evidence := eval(child, e)
		if o == decisive {
			return o, evidence
		}
		if o == Unknown {
			unknown = true
		}
		if i == 0 {
			firstEvidence = evidence
		}
	}
	if unknown {
		return Unknown, ""
	}
	return fallback, firstEvidence
}

// compare applies op to a stored value and a validated criteria value. ok is
// false when the stored value is null or not of the type op compares.
func compare(op string, got, want any) (met, ok bool) {
	switch op {
	case "==":
		switch w := want.(type) {
		case string:
			g, ok := got.(string)
			return g == w, ok
		case bool:
			g, ok := got.(bool)
			return g == w, ok
		case float64:
			g, ok := got.(float64)
			return g == w, ok
		}
	case "in":
		g, ok := got.(string)
		return slices.Contains(want.([]any), any(g)), ok
	case "contains_any":
		g, ok := got.([]any)
		return slices.ContainsFunc(want.([]any), func(w any) bool { return slices.Contains(g, w) }), ok
	case ">=":
		g, ok := got.(float64)
		return g >= want.(float64), ok
	case "<=":
		g, ok := got.(float64)
		return g <= want.(float64), ok
	case "overlaps":
		lo, hi, ok := pair(got)
		w := want.([]any)
		return lo <= w[1].(float64) && w[0].(float64) <= hi, ok
	}
	return false, false
}

// pair reads a stored [min, max] range.
func pair(v any) (lo, hi float64, ok bool) {
	p, ok := v.([]any)
	if !ok || len(p) != 2 {
		return 0, 0, false
	}
	lo, loOK := p[0].(float64)
	hi, hiOK := p[1].(float64)
	return lo, hi, loOK && hiOK
}

// compareRows orders full passes before rows with unknown must-haves, then
// by score, then known, then subject id.
func compareRows(a, b Row) int {
	return cmp.Or(
		cmp.Compare(boolInt(len(a.MustUnknown) > 0), boolInt(len(b.MustUnknown) > 0)),
		cmp.Compare(b.Score, a.Score),
		cmp.Compare(b.Known, a.Known),
		cmp.Compare(a.SubjectID, b.SubjectID),
	)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

`internal/core/search/schema/result.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Atlas match result, version 1",
  "type": "object",
  "required": ["version", "criteria_hash", "checked", "hidden", "rows"],
  "additionalProperties": false,
  "properties": {
    "version": {"const": 1},
    "criteria_hash": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    "checked": {"type": "integer", "minimum": 0},
    "hidden": {"type": "integer", "minimum": 0},
    "rows": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["subject_id", "score", "known", "results", "display"],
        "additionalProperties": false,
        "properties": {
          "subject_id": {"type": "string"},
          "score": {"type": "number", "minimum": 0, "maximum": 1},
          "known": {"type": "number", "minimum": 0, "maximum": 1},
          "must_unknown": {"type": "array", "minItems": 1, "items": {"type": "string"}},
          "results": {
            "type": "array",
            "items": {
              "type": "object",
              "required": ["id", "outcome"],
              "additionalProperties": false,
              "properties": {
                "id": {"type": "string"},
                "outcome": {"enum": ["met", "unmet", "unknown", "partial", "n/a"]},
                "evidence": {"type": "string"}
              }
            }
          },
          "display": {"type": "object"}
        }
      }
    }
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/search/ ./internal/arch/`
Expected: both `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/search/match.go internal/core/search/schema/result.json internal/core/search/match_test.go
git commit -m "search: match enrichments against criteria"
```

---

### Task 5: The job pack's declaration and examples

**Files:**
- Create: `packs/job-search/fields.json`, `packs/job-search/criteria.example.json`, `packs/job-search/examples/enrichment-full-pass.json`, `packs/job-search/examples/enrichment-must-unknown.json`, `packs/job-search/examples/enrichment-hidden.json`, `packs/job-search/examples/result.json`
- Test: `internal/core/search/packs_test.go`

**Interfaces:**
- Consumes: every exported name above; test helpers `mustRead` (Task 1), `resolve`, `envelope`, `instance` (Task 2).
- Produces: the files Session B (enricher prompt and schema), Session C (default criteria) and Session D (`requirements.*`) read.

`examples/result.json` is written by hand from the rules in spec section 4, not generated from `Match`: it is the independent statement of what `Match` must produce. Its `criteria_hash` is the sha256 of `criteria.example.json` exactly as committed; recompute it with `sha256sum packs/job-search/criteria.example.json` if that file's bytes change.

- [ ] **Step 1: Write the failing test**

`internal/core/search/packs_test.go`:

```go
package search_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

// TestEveryPackWithExamplesMatchesThem checks each pack that declares search
// fields: its criteria example parses against them, its example enrichments
// fit both schemas, and Match gives its expected result.
func TestEveryPackWithExamplesMatchesThem(t *testing.T) {
	decls, err := filepath.Glob("../../../packs/*/fields.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) == 0 {
		t.Fatal("no pack declares search fields")
	}
	for _, decl := range decls {
		dir := filepath.Dir(decl)
		t.Run(filepath.Base(dir), func(t *testing.T) { checkPack(t, dir) })
	}
}

func checkPack(t *testing.T, dir string) {
	f, err := search.ParseFields(mustRead(t, filepath.Join(dir, "fields.json")))
	if err != nil {
		t.Fatalf("fields.json: %v", err)
	}
	criteriaDoc := mustRead(t, filepath.Join(dir, "criteria.example.json"))
	if err := envelope(t, "criteria.json").Validate(instance(t, criteriaDoc)); err != nil {
		t.Errorf("criteria.example.json against the criteria schema: %v", err)
	}
	c, err := search.ParseCriteria(criteriaDoc, f)
	if err != nil {
		t.Fatalf("criteria.example.json: %v", err)
	}

	paths, err := filepath.Glob(filepath.Join(dir, "examples", "enrichment-*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples/enrichment-*.json: %v", err)
	}
	doc, values := envelope(t, "enrichment.json"), resolve(t, "generated", f.Schema())
	var es []search.Enrichment
	for _, path := range paths {
		data := mustRead(t, path)
		if err := doc.Validate(instance(t, data)); err != nil {
			t.Errorf("%s against the enrichment schema: %v", path, err)
		}
		var e search.Enrichment
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := values.Validate(instance(t, mustMarshal(t, e.Values))); err != nil {
			t.Errorf("%s values against the generated schema: %v", path, err)
		}
		es = append(es, e)
	}

	got := mustMarshal(t, search.Match(c, es))
	if err := envelope(t, "result.json").Validate(instance(t, got)); err != nil {
		t.Errorf("Match output against the result schema: %v", err)
	}
	want := instance(t, mustRead(t, filepath.Join(dir, "examples", "result.json")))
	if !reflect.DeepEqual(instance(t, got), want) {
		t.Errorf("Match result differs from examples/result.json; got:\n%s", got)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/search/ -run TestEveryPackWithExamplesMatchesThem`
Expected: FAIL, `no pack declares search fields`.

- [ ] **Step 3: Write the pack files**

`packs/job-search/fields.json`:

```json
{
  "version": 1,
  "fields": {
    "role.focus": {"kind": "scalar", "enum": ["backend", "platform", "fullstack", "frontend", "data", "ml", "mobile", "security", "other"]},
    "role.seniority": {"kind": "scalar", "enum": ["junior", "mid", "senior", "staff", "principal", "manager"]},
    "role.employment_type": {"kind": "scalar", "enum": ["full_time", "contract", "part_time", "internship"]},
    "remote.mode": {"kind": "scalar", "enum": ["remote", "hybrid", "onsite"]},
    "remote.eligible_regions": {"kind": "list", "pattern": "^([A-Z]{2}|AFRICA|EU|EMEA|AMERICAS|APAC|WORLDWIDE)$"},
    "remote.utc_offsets": {"kind": "range", "min": -12, "max": 14},
    "relocation.offered": {"kind": "bool"},
    "relocation.visa_sponsorship": {"kind": "bool"},
    "stack.languages": {"kind": "list", "canonical": true},
    "stack.platforms": {"kind": "list", "canonical": true},
    "salary.min": {"kind": "number"},
    "salary.max": {"kind": "number"},
    "salary.currency": {"kind": "scalar", "pattern": "^[A-Z]{3}$"},
    "salary.period": {"kind": "scalar", "enum": ["year", "month", "day", "hour"]},
    "signals.agent_native": {"kind": "bool"},
    "requirements.apply_url": {"kind": "text"},
    "requirements.deadline": {"kind": "text"},
    "requirements.documents": {"kind": "texts"},
    "requirements.screening_questions": {"kind": "texts"},
    "requirements.must_have_skills": {"kind": "texts"},
    "requirements.nice_to_have_skills": {"kind": "texts"}
  },
  "aliases": {
    "golang": "go",
    "node.js": "node",
    "nodejs": "node",
    "ts": "typescript",
    "js": "javascript",
    "k8s": "kubernetes",
    "postgres": "postgresql",
    "gcloud": "gcp",
    "otel": "opentelemetry",
    "amazon web services": "aws"
  }
}
```

`packs/job-search/criteria.example.json` (keep these bytes exactly; the expected result's hash depends on them):

```json
{
  "version": 1,
  "query": "senior backend, Go, remote or relocation, agent-native teams",
  "criteria": [
    {"id": "can-work-it", "statement": "Remote open to Nigeria, or relocation/visa offered",
     "must": true,
     "check": {"any": [
       {"field": "remote.eligible_regions", "op": "contains_any", "value": ["NG", "AFRICA", "EMEA", "WORLDWIDE"]},
       {"field": "relocation.offered", "op": "==", "value": true}]}},
    {"id": "focus", "statement": "Backend or platform role",
     "must": true,
     "check": {"field": "role.focus", "op": "in", "value": ["backend", "platform"]}},
    {"id": "timezone", "statement": "Within 3 hours of WAT, for remote roles",
     "weight": 1,
     "when": {"field": "remote.mode", "op": "==", "value": "remote"},
     "check": {"field": "remote.utc_offsets", "op": "overlaps", "value": [-2, 4]}},
    {"id": "seniority", "statement": "Senior or staff",
     "weight": 2,
     "check": {"field": "role.seniority", "op": "in", "value": ["senior", "staff"]}},
    {"id": "stack", "statement": "Go first, TypeScript second",
     "weight": 2,
     "check": {"field": "stack.languages", "op": "contains_any", "value": ["go"]},
     "partial": {"field": "stack.languages", "op": "contains_any", "value": ["typescript", "node"]}},
    {"id": "platform", "statement": "Kubernetes, cloud, observability",
     "weight": 1,
     "check": {"field": "stack.platforms", "op": "contains_any", "value": ["kubernetes", "gcp", "aws", "opentelemetry", "prometheus"]}},
    {"id": "agent-native", "statement": "Engineers already work with AI agents",
     "weight": 3,
     "check": {"field": "signals.agent_native", "op": "==", "value": true}}
  ],
  "display": ["salary", "role.employment_type"]
}
```

`packs/job-search/examples/enrichment-full-pass.json`:

```json
{
  "version": 1,
  "subject_id": "remoteok/all/1001",
  "content_hash": "a1",
  "enricher": {
    "engine": "claude-cli",
    "model": "claude-sonnet-5-5",
    "prompt_version": 1
  },
  "enriched_at": "2026-10-12T16:00:00Z",
  "values": {
    "role": {
      "focus": "platform",
      "seniority": "staff",
      "employment_type": "full_time"
    },
    "remote": {
      "mode": "remote",
      "eligible_regions": [
        "WORLDWIDE"
      ],
      "utc_offsets": [
        -1,
        3
      ]
    },
    "relocation": {
      "offered": null,
      "visa_sponsorship": null
    },
    "stack": {
      "languages": [
        "go"
      ],
      "platforms": [
        "kubernetes"
      ]
    },
    "salary": {
      "min": 120000,
      "max": 160000,
      "currency": "USD",
      "period": "year"
    },
    "signals": {
      "agent_native": null
    },
    "requirements": {
      "apply_url": "https://remoteok.com/remote-jobs/1001",
      "documents": [
        "cv"
      ],
      "screening_questions": null,
      "deadline": null,
      "must_have_skills": [
        "Go",
        "Kubernetes"
      ],
      "nice_to_have_skills": null
    }
  },
  "evidence": {
    "role.focus": "Staff Platform Engineer",
    "role.seniority": "Staff Platform Engineer",
    "role.employment_type": "Full-time",
    "remote.mode": "Fully remote",
    "remote.eligible_regions": "hire anywhere in the world",
    "remote.utc_offsets": "overlap with UTC-1 to UTC+3",
    "stack.languages": "services written in Go",
    "stack.platforms": "running on Kubernetes",
    "salary.min": "$120k - $160k",
    "salary.max": "$120k - $160k",
    "salary.currency": "$120k - $160k",
    "salary.period": "$120k - $160k per year"
  }
}
```

`packs/job-search/examples/enrichment-must-unknown.json`:

```json
{
  "version": 1,
  "subject_id": "greenhouse/gitlab/8490477002",
  "content_hash": "b2",
  "enricher": {
    "engine": "claude-cli",
    "model": "claude-sonnet-5-5",
    "prompt_version": 1
  },
  "enriched_at": "2026-10-12T16:00:00Z",
  "values": {
    "role": {
      "focus": "backend",
      "seniority": "staff",
      "employment_type": "full_time"
    },
    "remote": {
      "mode": "remote",
      "eligible_regions": [
        "CA",
        "GB",
        "US"
      ],
      "utc_offsets": null
    },
    "relocation": {
      "offered": null,
      "visa_sponsorship": null
    },
    "stack": {
      "languages": [
        "ruby",
        "go"
      ],
      "platforms": [
        "kubernetes",
        "gcp"
      ]
    },
    "salary": {
      "min": null,
      "max": null,
      "currency": null,
      "period": null
    },
    "signals": {
      "agent_native": null
    },
    "requirements": {
      "apply_url": "https://job-boards.greenhouse.io/gitlab/jobs/8490477002",
      "documents": [
        "cv"
      ],
      "screening_questions": [
        "Are you located in one of the listed countries?"
      ],
      "deadline": null,
      "must_have_skills": [
        "Ruby on Rails",
        "PostgreSQL"
      ],
      "nice_to_have_skills": [
        "Go"
      ]
    }
  },
  "evidence": {
    "role.focus": "Staff Backend Engineer",
    "role.seniority": "Staff Backend Engineer",
    "role.employment_type": "full-time",
    "remote.mode": "Remote, Canada; Remote, United Kingdom; Remote, US",
    "remote.eligible_regions": "Remote, Canada; Remote, United Kingdom; Remote, US",
    "stack.languages": "Ruby on Rails and Go",
    "stack.platforms": "Kubernetes on GCP"
  }
}
```

`packs/job-search/examples/enrichment-hidden.json`:

```json
{
  "version": 1,
  "subject_id": "lever/palantir/2002",
  "content_hash": "c3",
  "enricher": {
    "engine": "claude-cli",
    "model": "claude-sonnet-5-5",
    "prompt_version": 1
  },
  "enriched_at": "2026-10-12T16:00:00Z",
  "values": {
    "role": {
      "focus": "frontend",
      "seniority": "senior",
      "employment_type": "full_time"
    },
    "remote": {
      "mode": "onsite",
      "eligible_regions": null,
      "utc_offsets": null
    },
    "relocation": {
      "offered": true,
      "visa_sponsorship": true
    },
    "stack": {
      "languages": [
        "typescript"
      ],
      "platforms": null
    },
    "salary": {
      "min": null,
      "max": null,
      "currency": null,
      "period": null
    },
    "signals": {
      "agent_native": true
    },
    "requirements": {
      "apply_url": "https://jobs.lever.co/palantir/2002",
      "documents": [
        "cv",
        "cover_letter"
      ],
      "screening_questions": null,
      "deadline": null,
      "must_have_skills": [
        "React",
        "TypeScript"
      ],
      "nice_to_have_skills": null
    }
  },
  "evidence": {
    "role.focus": "Senior Frontend Engineer",
    "role.seniority": "Senior Frontend Engineer",
    "role.employment_type": "Full-time",
    "remote.mode": "on-site in London",
    "relocation.offered": "relocation support provided",
    "relocation.visa_sponsorship": "we sponsor visas",
    "stack.languages": "TypeScript and React",
    "signals.agent_native": "our engineers pair with Claude Code"
  }
}
```

`packs/job-search/examples/result.json`:

```json
{
  "version": 1,
  "criteria_hash": "9964001339010cc864ce0dfb880a90a7d880f049426e3a65f521970e6579362e",
  "checked": 3,
  "hidden": 1,
  "rows": [
    {
      "subject_id": "remoteok/all/1001",
      "score": 0.6666666666666666,
      "known": 0.6666666666666666,
      "results": [
        {"id": "can-work-it", "outcome": "met", "evidence": "hire anywhere in the world"},
        {"id": "focus", "outcome": "met", "evidence": "Staff Platform Engineer"},
        {"id": "timezone", "outcome": "met", "evidence": "overlap with UTC-1 to UTC+3"},
        {"id": "seniority", "outcome": "met", "evidence": "Staff Platform Engineer"},
        {"id": "stack", "outcome": "met", "evidence": "services written in Go"},
        {"id": "platform", "outcome": "met", "evidence": "running on Kubernetes"},
        {"id": "agent-native", "outcome": "unknown"}
      ],
      "display": {
        "role.employment_type": "full_time",
        "salary": {"min": 120000, "max": 160000, "currency": "USD", "period": "year"}
      }
    },
    {
      "subject_id": "greenhouse/gitlab/8490477002",
      "score": 0.5555555555555556,
      "known": 0.5555555555555556,
      "must_unknown": ["can-work-it"],
      "results": [
        {"id": "can-work-it", "outcome": "unknown"},
        {"id": "focus", "outcome": "met", "evidence": "Staff Backend Engineer"},
        {"id": "timezone", "outcome": "unknown"},
        {"id": "seniority", "outcome": "met", "evidence": "Staff Backend Engineer"},
        {"id": "stack", "outcome": "met", "evidence": "Ruby on Rails and Go"},
        {"id": "platform", "outcome": "met", "evidence": "Kubernetes on GCP"},
        {"id": "agent-native", "outcome": "unknown"}
      ],
      "display": {
        "role.employment_type": "full_time",
        "salary": {"min": null, "max": null, "currency": null, "period": null}
      }
    }
  ]
}
```

- [ ] **Step 4: Run the whole suite**

Run: `sha256sum packs/job-search/criteria.example.json && go vet ./... && go test ./...`
Expected: the hash is `9964001339010cc864ce0dfb880a90a7d880f049426e3a65f521970e6579362e`; every package `ok`, including `internal/arch` and `internal/adapters/outbound/tools` (`TestPackTemplateFixturesRender` must not see the examples).

- [ ] **Step 5: Commit**

```bash
git add packs/job-search internal/core/search/packs_test.go
git commit -m "packs: declare the job search fields and their worked examples"
```

---

### Task 6: Increment note

**Files:**
- Create: `docs/notes/2026-10-13-search-contracts.md`

- [ ] **Step 1: Write the note**

Four headed sections, short, from what this increment actually showed. Seed content to verify and extend:

- **The pattern:** the harness/pack split applied to search. Go owns the criteria language, the envelope, `Match` and schema generation; a pack owns the field table. One declaration drives criteria validation and the model's output schema, so the two cannot drift.
- **What surprised:** the first spec put the job field table in Go, and nothing in design review caught it; `TestGoTreeIsFreeOfUseCaseVocabulary` did, on the first prototype. Also: `packs/*/testdata/` was already claimed by the render fixture test, and `json.Unmarshal` silently dropped a misspelt `"partail"`.
- **What I would do differently:** run the architecture tests against a throwaway prototype before writing a spec's deliverable section.
- **What I still do not understand:** how well a model fills `canonical` lists before `Normalize` runs, and how often `signals.agent_native` can be evidenced from a posting at all. Session B measures both.

- [ ] **Step 2: Commit**

```bash
git add docs/notes/2026-10-13-search-contracts.md
git commit -m "docs: note the search contracts increment"
```

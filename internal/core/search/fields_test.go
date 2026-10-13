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
	cases := map[string]struct{ doc, want string }{
		"bad pattern":       {`{"version": 1, "fields": {"a": {"kind": "scalar", "pattern": "("}}}`, `pattern: error parsing regexp`},
		"uppercase path":    {`{"version": 1, "fields": {"Loaf.grain": {"kind": "scalar"}}}`, `path must match`},
		"wrong version":     {`{"version": 2, "fields": {}}`, `version 2 is not supported`},
		"misspelt key":      {`{"version": 1, "fields": {"a": {"kind": "scalar", "enun": ["x"]}}}`, `"enun"`},
		"wrong-case key":    {`{"Version": 1, "fields": {}}`, `"Version"`},
		"trailing data":     {`{"version": 1, "fields": {}} {"version": 1}`, `after top-level value`},
		"enum on text":      {`{"version": 1, "fields": {"a": {"kind": "text", "enum": ["x"]}}}`, `enum, pattern and canonical apply only to scalar and list`},
		"pattern on number": {`{"version": 1, "fields": {"a": {"kind": "number", "pattern": "^x$"}}}`, `enum, pattern and canonical apply only to scalar and list`},
		"canonical on bool": {`{"version": 1, "fields": {"a": {"kind": "bool", "canonical": true}}}`, `enum, pattern and canonical apply only to scalar and list`},
		"min on a scalar":   {`{"version": 1, "fields": {"a": {"kind": "scalar", "min": 0}}}`, `min and max apply only to range`},
		"min above max":     {`{"version": 1, "fields": {"a": {"kind": "range", "min": 10, "max": 0}}}`, `min is above max`},
		"uppercase alias key": {`{"version": 1, "fields": {}, "aliases": {"Golang": "go"}}`,
			`alias "Golang": key is not trimmed lowercase`},
		"uncanonical alias target": {`{"version": 1, "fields": {}, "aliases": {"golang": "Go"}}`,
			`alias "golang": target "Go" is not trimmed lowercase`},
		"alias chain": {`{"version": 1, "fields": {}, "aliases": {"a": "b", "b": "c"}}`,
			`alias "a": target "b" is itself an alias`},
	}
	for name, tc := range cases {
		_, err := search.ParseFields([]byte(tc.doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %s", name, err, tc.want)
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

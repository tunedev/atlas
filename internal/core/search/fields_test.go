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

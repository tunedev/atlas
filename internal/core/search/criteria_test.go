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
	const rye = `"check": {"field": "loaf.grain", "op": "==", "value": "rye"}`
	cases := map[string]struct{ doc, want string }{
		"repeated id": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, ` + rye + `},
		  {"id": "a", "statement": "s", "weight": 1, ` + rye + `}]}`, `id repeats`},
		"must and weight": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "must": true, "weight": 1, ` + rye + `}]}`, `needs exactly one of must and weight`},
		"must and a zero weight": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "must": true, "weight": 0, ` + rye + `}]}`, `needs exactly one of must and weight`},
		"neither must nor weight": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", ` + rye + `}]}`, `needs exactly one of must and weight`},
		"zero weight": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 0, ` + rye + `}]}`, `weight must be positive`},
		"empty statement": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": " ", "weight": 1, ` + rye + `}]}`, `statement is empty`},
		"op does not fit the kind": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.toppings", "op": "==", "value": "sesame"}}]}`,
			`op "==" does not apply to loaf.toppings`},
		"uncanonical value": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.toppings", "op": "contains_any", "value": ["sesame seeds"]}}]}`,
			`value sesame seeds is outside the vocabulary`},
		"value outside the pattern": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "price.currency", "op": "==", "value": "euro"}}]}`,
			`value euro is outside the vocabulary`},
		"range outside the bounds": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [20, 26]}}]}`,
			`is not an integer range within 0 to 24`},
		"backwards range": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "open.hours", "op": "overlaps", "value": [9, 6]}}]}`,
			`is not an integer range within 0 to 24`},
		"comparison and combinator together": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "==", "value": "rye",
		   "any": [{"field": "loaf.grain", "op": "==", "value": "spelt"}]}}]}`,
			`a condition is one comparison, one any, or one all`},
		"empty condition": {`{"version": 1, "criteria": [{"id": "a", "statement": "s", "weight": 1, "check": {}}]}`,
			`empty condition`},
		"undeclared display":      {`{"version": 1, "criteria": [], "display": ["crust"]}`, `display "crust" is not a declared field`},
		"partial segment display": {`{"version": 1, "criteria": [], "display": ["pri"]}`, `display "pri" is not a declared field`},
		"empty in list": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, "check": {"field": "loaf.grain", "op": "in", "value": []}}]}`,
			`is not a non-empty list`},
		"misspelt key": {`{"version": 1, "criteria": [
		  {"id": "a", "statement": "s", "weight": 1, ` + rye + `,
		   "partail": {"field": "loaf.grain", "op": "==", "value": "spelt"}}]}`, `"partail"`},
		"wrong-case key": {`{"version": 1, "Criteria": []}`, `"Criteria"`},
		"trailing data":  {`{"version": 1, "criteria": []} {"version": 1}`, `after top-level value`},
		"wrong version":  {`{"version": 2, "criteria": []}`, `version 2 is not supported`},
	}
	f := bakery(t)
	for name, tc := range cases {
		_, err := search.ParseCriteria([]byte(tc.doc), f)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %s", name, err, tc.want)
		}
	}
}

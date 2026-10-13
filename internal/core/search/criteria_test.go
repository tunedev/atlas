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

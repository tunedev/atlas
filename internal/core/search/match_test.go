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

func TestARowWithNoCriteriaFitsTheResultSchema(t *testing.T) {
	c := criteria(t, `{"version": 1, "criteria": [], "display": ["loaf.grain"]}`)
	got := mustMarshal(t, search.Match(c, []search.Enrichment{subject(t, "s", `{}`)}))
	if err := envelope(t, "result.json").Validate(instance(t, got)); err != nil {
		t.Errorf("Match output against the result schema: %v\n%s", err, got)
	}
}

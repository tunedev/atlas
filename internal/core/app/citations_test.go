package app_test

import (
	"encoding/json"
	"reflect"
	"strings"
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

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
	  {"text":"never judged","citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]},
	  {"text":null,"citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]},
	  {"text":7,"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded","relevant":true}]},
	  {"text":"","citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]}],
	 "picked":[
	  {"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}]},
	  {"citations":[{"id":7,"quote":"","status":"needs_review"}]}]
	}`)
	s := app.Settle(tree)
	got, kept, gaps := encode(t, s.Tree), s.Kept, s.Gaps
	for _, want := range []string{
		`{"citations":[{"id":0,"quote":"Logged every passing ship","relevant":true,"status":"grounded"}],"gap":false,"text":"keeps a ship log"}`,
		`{"citations":[],"gap":true,"text":"sails the ship"}`,
		`{"citations":[],"gap":true,"text":"never judged"}`,
		`{"citations":[],"gap":true,"text":null}`,
		`{"citations":[],"gap":true,"text":7}`,
		`{"citations":[],"gap":true,"text":""}`,
		`{"citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}],"gap":false}`,
		`{"citations":[],"gap":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settled lacks %s:\n%s", want, got)
		}
	}
	if gaps != 6 {
		t.Errorf("gaps = %d; want 6", gaps)
	}
	wantKept := map[string][]string{"claims": {"keeps a ship log"}, "picked": {"Refitted the lamp lens"}}
	if empty := app.Settle(decode(t, `{"none":[{"citations":[]}]}`)).Kept; !reflect.DeepEqual(empty, map[string][]string{"none": {}}) {
		t.Errorf("a key with nothing kept must still be present, empty: %v", empty)
	}
	if !reflect.DeepEqual(kept, wantKept) {
		t.Errorf("kept = %v; want %v", kept, wantKept)
	}
}

func TestSettleTreatsEveryTextObjectAsAStatement(t *testing.T) {
	grounded := `{"id":0,"quote":"Logged every passing ship","status":"grounded","relevant":true}`
	tree := decode(t, `{"claims":[
	  {"text":"x","gap":false},
	  {"text":"   ","citations":[`+grounded+`]},
	  {"text":"  keeps a ship log  ","citations":[`+grounded+`],"gap":true}]}`)
	s := app.Settle(tree)
	got := encode(t, s.Tree)
	for _, want := range []string{
		`{"citations":[],"gap":true,"text":"x"}`,
		`{"citations":[],"gap":true,"text":""}`,
		`"gap":false,"text":"keeps a ship log"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settled lacks %s:\n%s", want, got)
		}
	}
	if s.Gaps != 2 {
		t.Errorf("gaps = %d; want 2", s.Gaps)
	}
	if want := []string{"keeps a ship log"}; !reflect.DeepEqual(s.Kept["claims"], want) {
		t.Errorf("kept = %v; want %v", s.Kept["claims"], want)
	}
}

func TestSettleListsTheTextOfEveryGapPerKeyAndInAll(t *testing.T) {
	tree := decode(t, `{
	 "asked":[{"text":"sails a tall ship","citations":[]},{"text":"keeps a log","citations":[{"quote":"Logged","status":"grounded","relevant":true}]}],
	 "picked":[{"citations":[]}],
	 "said":[{"text":"I captained a ship.","citations":[]},{"text":"","citations":[]}]}`)
	s := app.Settle(tree)
	wantText := map[string][]string{
		"asked":  {"sails a tall ship"},
		"picked": {},
		"said":   {"I captained a ship."},
	}
	if !reflect.DeepEqual(s.GapsText, wantText) {
		t.Errorf("gaps text = %v; want %v", s.GapsText, wantText)
	}
	if want := []string{"sails a tall ship", "I captained a ship."}; !reflect.DeepEqual(s.GapsAll, want) {
		t.Errorf("gaps all = %v; want %v", s.GapsAll, want)
	}
}

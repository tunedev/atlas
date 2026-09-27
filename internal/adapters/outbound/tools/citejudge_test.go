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
// yes, and no otherwise. fail makes a call error: every call if failOn is 0,
// or only the failOn'th call (1-indexed) otherwise.
type relevanceJudge struct {
	yes      []string
	fail     error
	failOn   int
	subjects []string
}

func (r *relevanceJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	r.subjects = append(r.subjects, subject)
	if r.fail != nil && (r.failOn == 0 || len(r.subjects) == r.failOn) {
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

// partialAnswerJudge answers only the first of however many questions it is
// asked, so the caller sees a question with no matching answer.
type partialAnswerJudge struct{}

func (partialAnswerJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	return ports.Judgement{Subject: subject, Model: "stub", Provider: "stub", When: time.Now(), Answers: []ports.Answer{
		{ID: qs[0].ID, Kind: ports.KindNoul, Chosen: "yes", Distribution: map[string]float64{"yes": 0.9, "no": 0.1}},
	}}, nil
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

const twoClaimsGroundedFields = `{"claims":[
 {"text":"keeps a ship log","citations":[{"id":0,"quote":"Logged every passing ship","status":"grounded"}]},
 {"text":"refits the lamp","citations":[{"id":1,"quote":"Refitted the lamp lens","status":"grounded"}]}
]}`

func TestJudgeErrorFailsTheStepAndMarksNothingRelevant(t *testing.T) {
	docs, index := store(t)
	j := &relevanceJudge{yes: []string{"ship"}, fail: errors.New("engine down"), failOn: 2}
	out, err := tools.NewCitationsJudge(j, docs, index).
		Invoke(context.Background(), map[string]string{"fields": twoClaimsGroundedFields, "threshold": "0.6", "subject_id": "keeper"})
	if err == nil || !strings.Contains(err.Error(), "engine down") {
		t.Errorf("err = %v; want the engine's error", err)
	}
	if out != nil {
		t.Errorf("out = %v; want nil, no partially marked tree escaping a failed step", out)
	}
}

func TestMissingAnswerFailsTheStep(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewCitationsJudge(partialAnswerJudge{}, docs, index).
		Invoke(context.Background(), map[string]string{"fields": groundedFields, "threshold": "0.6", "subject_id": "keeper"})
	if err == nil || !strings.Contains(err.Error(), "no answer for c1") {
		t.Errorf("err = %v; want an error naming the unanswered question", err)
	}
}

func TestCitationsJudgeRejectsBadInput(t *testing.T) {
	docs, index := store(t)
	for name, with := range map[string]map[string]string{
		"bad json":      {"fields": "{", "threshold": "0.5", "subject_id": "s"},
		"bad threshold": {"fields": "{}", "threshold": "high", "subject_id": "s"},
		"out of range":  {"fields": "{}", "threshold": "1.5", "subject_id": "s"},
		"zero":          {"fields": "{}", "threshold": "0", "subject_id": "s"},
		"nan":           {"fields": "{}", "threshold": "NaN", "subject_id": "s"},
		"no subject":    {"fields": "{}", "threshold": "0.5", "subject_id": ""},
	} {
		if _, err := tools.NewCitationsJudge(&relevanceJudge{}, docs, index).Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

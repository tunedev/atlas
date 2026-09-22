package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

var errFailed = errors.New("judge is down")

type stubJudge struct {
	calls   int
	subject string
	asked   []ports.Question
	fail    error
}

func (s *stubJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	s.calls++
	s.subject = subject
	s.asked = qs
	if s.fail != nil {
		return ports.Judgement{}, s.fail
	}
	answers := make([]ports.Answer, 0, len(qs))
	for _, q := range qs {
		answers = append(answers, ports.Answer{
			ID: q.ID, Kind: q.Kind, Chosen: "yes",
			Distribution: map[string]float64{"yes": 0.94, "no": 0.06},
			Expected:     1.5,
		})
	}
	return ports.Judgement{Subject: subject, Model: "a-model", When: time.Now().UTC(), Answers: answers}, nil
}

func store(t *testing.T) (*gitdocs.Store, *sqlindex.Index) {
	t.Helper()
	ctx := context.Background()
	docs, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	index, err := sqlindex.Open(ctx, t.TempDir()+"/index.db")
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return docs, index
}

const questionsYAML = `
- id: readable
  type: noul
  ask: Is it readable?
- id: genre
  type: choice
  options: [fiction, history, poetry]
  ask: Which genre is it?
- id: length
  type: score
  levels: [short, medium, long]
  ask: How long is it?
`

func TestTheToolParsesEveryQuestionKind(t *testing.T) {
	j := &stubJudge{}
	docs, index := store(t)

	_, err := tools.NewJudge(j, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1",
		"subject":    "a short book",
		"questions":  questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if j.calls != 1 {
		t.Errorf("judge called %d times, want 1", j.calls)
	}
	if len(j.asked) != 3 {
		t.Fatalf("questions asked = %d, want 3", len(j.asked))
	}
	byID := map[string]ports.Question{}
	for _, q := range j.asked {
		byID[q.ID] = q
	}
	if byID["readable"].Kind != ports.KindNoul {
		t.Errorf("readable kind = %q", byID["readable"].Kind)
	}
	if len(byID["genre"].Options) != 3 {
		t.Errorf("choice options did not reach the judge: %+v", byID["genre"])
	}
	if len(byID["length"].Options) != 3 {
		t.Errorf("score levels did not reach the judge as options: %+v", byID["length"])
	}
	if byID["genre"].Ask == "" {
		t.Error("the question text did not reach the judge")
	}
}

func TestTheResultIsKeyedByQuestionIDForLaterSteps(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %T, want map[string]any", out)
	}
	readable, ok := m["readable"].(map[string]any)
	if !ok {
		t.Fatalf("no answer keyed by question id: %+v", m)
	}
	if readable["chosen"] != "yes" {
		t.Errorf("chosen = %v", readable["chosen"])
	}
	p, ok := readable["p"].(float64)
	if !ok || p < 0.9 {
		t.Errorf("p = %v, want the chosen option's mass", readable["p"])
	}
	if _, ok := readable["distribution"]; !ok {
		t.Error("the distribution is not available to a later step")
	}
	if m["path"] == nil || m["path"] == "" {
		t.Error("the tool does not report where the judgement was recorded")
	}
}

func TestTheJudgementIsOnDiskWithAnEmptyOutcome(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	path := out.(map[string]any)["path"].(string)

	body, err := docs.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}
	if outcome, present := doc["outcome"]; !present || outcome != nil {
		t.Errorf("outcome = %v present = %v, want a present null slot", outcome, present)
	}

	rows, err := index.Find(context.Background(), ports.Query{Kind: "judgement", Limit: 10})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 || rows[0].Fields["subject_id"] != "subject-1" {
		t.Errorf("the judgement is not findable in the index: %+v", rows)
	}
}

func TestAMissingSubjectIDIsAnError(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject": "a short book", "questions": questionsYAML,
	})
	if err == nil {
		t.Fatal("a judgement with no subject id was accepted; nothing could attach an outcome to it later")
	}
	if !strings.Contains(err.Error(), "judge.ask: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
}

func TestMalformedQuestionsAreAnErrorNotAnEmptyJudgement(t *testing.T) {
	docs, index := store(t)
	for _, tc := range []struct{ name, questions string }{
		{"not yaml", "id: [unclosed"},
		{"no questions", ""},
		{"missing id", "- type: noul\n  ask: Is it readable?"},
		{"unknown type", "- id: x\n  type: vibes\n  ask: Well?"},
		{"too few options", "- id: x\n  type: choice\n  options: [only]\n  ask: Well?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
				"subject_id": "subject-1", "subject": "a short book", "questions": tc.questions,
			})
			if err == nil {
				t.Fatal("malformed questions produced a judgement")
			}
		})
	}
}

func TestAQuestionIDOfPathIsRejected(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book",
		"questions": "- id: path\n  type: noul\n  ask: Well?",
	})
	if err == nil {
		t.Fatalf("a question id of path was accepted; its answer would be overwritten by the tool's own path key: %+v", out)
	}
	if !strings.Contains(err.Error(), "judge.ask: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error does not say the id is reserved: %v", err)
	}
}

func TestAJudgeFailureRecordsNothing(t *testing.T) {
	docs, index := store(t)
	j := &stubJudge{fail: errFailed}
	_, err := tools.NewJudge(j, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err == nil {
		t.Fatal("a failing judge produced a result")
	}
	rows, findErr := index.Find(context.Background(), ports.Query{Kind: "judgement", Limit: 10})
	if findErr != nil {
		t.Fatalf("find: %v", findErr)
	}
	if len(rows) != 0 {
		t.Errorf("a failed judgement was recorded anyway: %+v", rows)
	}
}

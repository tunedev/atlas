package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type fakeDocs struct {
	put map[string][]byte
}

func newFakeDocs() *fakeDocs { return &fakeDocs{put: map[string][]byte{}} }

func (d *fakeDocs) Put(_ context.Context, path string, body []byte, _ string) (ports.Revision, error) {
	d.put[path] = body
	return ports.Revision("rev-" + path), nil
}
func (d *fakeDocs) Get(_ context.Context, path string) ([]byte, error)           { return d.put[path], nil }
func (d *fakeDocs) List(_ context.Context, _ string) ([]string, error)           { return nil, nil }
func (d *fakeDocs) History(_ context.Context, _ string) ([]ports.DocMeta, error) { return nil, nil }
func (d *fakeDocs) GetAt(_ context.Context, path string, _ ports.Revision) ([]byte, error) {
	return d.put[path], nil
}

type fakeIndex struct{ rows []ports.Record }

func (i *fakeIndex) Upsert(_ context.Context, r ports.Record) error {
	i.rows = append(i.rows, r)
	return nil
}
func (i *fakeIndex) Find(_ context.Context, _ ports.Query) ([]ports.Record, error) {
	return i.rows, nil
}
func (i *fakeIndex) Reset(_ context.Context) error { return nil }
func (i *fakeIndex) Close() error                  { return nil }

func aJudgement() ports.Judgement {
	return ports.Judgement{
		Subject: "a short book",
		Model:   "a-model",
		When:    time.Date(2026, 9, 22, 11, 4, 2, 0, time.UTC),
		Answers: []ports.Answer{{
			ID:           "readable",
			Kind:         ports.KindNoul,
			Chosen:       "yes",
			Distribution: map[string]float64{"yes": 0.94, "no": 0.06},
		}},
	}
}

func recordQuestions() []ports.Question {
	return []ports.Question{{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"}}
}

func TestTheDocumentCarriesTheQuestionTheAnswerAndAnEmptyOutcome(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}

	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	body, ok := docs.put[path]
	if !ok {
		t.Fatalf("nothing was written at the returned path %q", path)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}
	if doc["subject_id"] != "subject-1" {
		t.Errorf("document does not carry the subject id: %+v", doc["subject_id"])
	}
	outcome, present := doc["outcome"]
	if !present {
		t.Error("the document has no outcome slot; a later increment has nowhere to record what happened")
	}
	if outcome != nil {
		t.Errorf("outcome = %v, want null until an outcome is known", outcome)
	}
	if _, present := doc["questions"]; !present {
		t.Error("the document does not record the questions as asked")
	}
	if _, present := doc["answers"]; !present {
		t.Error("the document does not record the answers")
	}
}

func TestThePathIsKeyedBySubjectSoJudgementsOfOneSubjectSitTogether(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !strings.HasPrefix(path, "judgements/subject-1/") {
		t.Errorf("path = %q, want it under judgements/subject-1/", path)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("path = %q, want a .json document", path)
	}
}

func TestJudgingTheSameSubjectTwiceDoesNotOverwriteTheFirst(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	first := aJudgement()
	second := aJudgement()
	second.When = first.When.Add(time.Minute)

	p1, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	p2, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both judgements landed at %q; the first is gone", p1)
	}
	if len(docs.put) != 2 {
		t.Errorf("documents written = %d, want 2", len(docs.put))
	}
}

func TestTheIndexRowMakesAPendingJudgementFindable(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(index.rows) != 1 {
		t.Fatalf("index rows = %d, want 1", len(index.rows))
	}
	row := index.rows[0]
	if row.Kind != "judgement" {
		t.Errorf("kind = %q, want judgement", row.Kind)
	}
	if row.Fields["subject_id"] != "subject-1" {
		t.Errorf("fields do not carry the subject id: %+v", row.Fields)
	}
	if row.Fields["outcome"] != "pending" {
		t.Errorf("outcome field = %q, want pending; an awaiting judgement must be findable by query", row.Fields["outcome"])
	}
	if row.Rev == "" {
		t.Error("the index row does not carry the revision the document was written at")
	}
}

func TestAnEmptySubjectIDIsAnError(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordJudgement(context.Background(), docs, index, "", recordQuestions(), aJudgement()); err == nil {
		t.Fatal("a judgement with no subject id was recorded; nothing could ever attach an outcome to it")
	}
}

func TestAJudgementWithNoAnswersIsAnError(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	empty := aJudgement()
	empty.Answers = nil
	if _, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), empty); err == nil {
		t.Fatal("an empty judgement was recorded")
	}
}

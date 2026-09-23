package app_test

import (
	"context"
	"encoding/json"
	"errors"
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
		Subject:  "a short book",
		Model:    "a-model",
		Provider: "a-provider",
		Sampling: ports.Sampling{Temperature: 0, Seed: 7, TopLogProbs: 5, MaxTokens: 128},
		When:     time.Date(2026, 9, 22, 11, 4, 2, 0, time.UTC),
		Answers: []ports.Answer{{
			ID:           "readable",
			Kind:         ports.KindNoul,
			Chosen:       "yes",
			Distribution: map[string]float64{"yes": 0.94, "no": 0.06},
			Alternatives: []ports.Alternative{{Text: "Yes", LogProb: -0.4}, {Text: "no", LogProb: -3.1}},
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

// TestTheDocumentCarriesProviderSamplingAndAlternatives covers F2 and F3:
// the document records what produced the probability (provider, sampling)
// and what it was read from (the raw alternatives), none of which can be
// added retroactively to a judgement already made.
func TestTheDocumentCarriesProviderSamplingAndAlternatives(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}

	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(docs.put[path], &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}

	if doc["provider"] != "a-provider" {
		t.Errorf("provider = %v, want a-provider", doc["provider"])
	}
	sampling, ok := doc["sampling"].(map[string]any)
	if !ok {
		t.Fatalf("no sampling recorded: %+v", doc)
	}
	if sampling["seed"] != float64(7) || sampling["top_logprobs"] != float64(5) || sampling["max_tokens"] != float64(128) {
		t.Errorf("sampling = %+v, want seed=7 top_logprobs=5 max_tokens=128", sampling)
	}

	answers, ok := doc["answers"].([]any)
	if !ok || len(answers) != 1 {
		t.Fatalf("answers = %+v, want one answer", doc["answers"])
	}
	answer := answers[0].(map[string]any)
	alts, ok := answer["alternatives"].([]any)
	if !ok || len(alts) != 2 {
		t.Fatalf("alternatives = %+v, want the two alternatives read from the answer token", answer["alternatives"])
	}
	first := alts[0].(map[string]any)
	if first["text"] != "Yes" {
		t.Errorf("first alternative = %+v, want text Yes", first)
	}
}

// TestTheIndexRowCarriesTheProvider covers F3's index-row half.
func TestTheIndexRowCarriesTheProvider(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(index.rows) != 1 {
		t.Fatalf("index rows = %d, want 1", len(index.rows))
	}
	if got := index.rows[0].Fields["provider"]; got != "a-provider" {
		t.Errorf("provider field = %q, want a-provider", got)
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

// TestAZeroExpectedScoreIsStillPresentInTheDocument covers the minor at
// judgementrecord.go:34: omitempty on Expected used to drop the key when
// all mass sat on the first level (Expected == 0), which is a real answer,
// not a missing field.
func TestAZeroExpectedScoreIsStillPresentInTheDocument(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	j := aJudgement()
	j.Answers = []ports.Answer{{
		ID: "length", Kind: ports.KindScore, Chosen: "short",
		Distribution: map[string]float64{"short": 1, "medium": 0, "long": 0},
		Expected:     0,
	}}

	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), j)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(docs.put[path], &doc); err != nil {
		t.Fatalf("document is not valid json: %v", err)
	}
	answer := doc["answers"].([]any)[0].(map[string]any)
	if _, present := answer["expected"]; !present {
		t.Error("expected key is missing for a zero score; omitempty dropped a real answer")
	}
}

// TestTheDocumentWhenHasMillisecondPrecision covers the minor at
// judgementrecord.go:114 vs :18: the document's "when" used second
// precision while the path used milliseconds, so two judgements 200ms
// apart were unorderable from the document alone.
func TestTheDocumentWhenHasMillisecondPrecision(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	first := aJudgement()
	second := aJudgement()
	second.When = first.When.Add(200 * time.Millisecond)

	p1, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	p2, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}

	var doc1, doc2 map[string]any
	if err := json.Unmarshal(docs.put[p1], &doc1); err != nil {
		t.Fatalf("document 1 is not valid json: %v", err)
	}
	if err := json.Unmarshal(docs.put[p2], &doc2); err != nil {
		t.Fatalf("document 2 is not valid json: %v", err)
	}
	if doc1["when"] == doc2["when"] {
		t.Errorf("when = %v for both documents, 200ms apart; the document cannot order them", doc1["when"])
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

func TestJudgingTheSameSubjectWithinASecondProducesDistinctPaths(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	first := aJudgement()
	second := aJudgement()
	second.When = first.When.Add(200 * time.Millisecond)

	p1, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	p2, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both judgements landed at %q; a sub-second gap collided", p1)
	}
	if _, ok := docs.put[p1]; !ok {
		t.Errorf("first judgement at %q did not survive", p1)
	}
	if _, ok := docs.put[p2]; !ok {
		t.Errorf("second judgement at %q did not survive", p2)
	}
}

type failingIndex struct{ err error }

func (i *failingIndex) Upsert(_ context.Context, _ ports.Record) error { return i.err }
func (i *failingIndex) Find(_ context.Context, _ ports.Query) ([]ports.Record, error) {
	return nil, nil
}
func (i *failingIndex) Reset(_ context.Context) error { return nil }
func (i *failingIndex) Close() error                  { return nil }

func TestAFailedUpsertReturnsTheWrittenPathAndAnError(t *testing.T) {
	docs := newFakeDocs()
	index := &failingIndex{err: errors.New("index unavailable")}

	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err == nil {
		t.Fatal("a failed upsert was not reported as an error")
	}
	if path == "" {
		t.Fatal("no path was returned; the document was already written and the caller needs to know where")
	}
	if _, ok := docs.put[path]; !ok {
		t.Errorf("returned path %q does not match a document actually written", path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the written path %q", err.Error(), path)
	}
}

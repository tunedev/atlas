package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// record opens a real git record and SQLite index over temp directories.
func record(t *testing.T) (*gitdocs.Store, *sqlindex.Index) {
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

func rainQuestion() []ports.Question {
	return []ports.Question{{ID: "rain", Kind: ports.KindNoul, Ask: "Will it rain on the day?"}}
}

// forecast is a judgement of subject that it will rain with probability p.
func forecast(subject string, p float64, when time.Time) ports.Judgement {
	return ports.Judgement{
		Subject:  subject,
		Model:    "a-model",
		Provider: "a-provider",
		When:     when,
		Answers: []ports.Answer{{
			ID: "rain", Kind: ports.KindNoul, Chosen: "yes",
			Distribution: map[string]float64{"yes": p, "no": 1 - p},
			Coverage:     ports.Coverage{Represented: 2, Declared: 2},
		}},
	}
}

var day = time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)

func wet() app.Outcome {
	return app.Outcome{State: "wet", When: day.Add(48 * time.Hour), Note: "umbrella needed"}
}

func judged(t *testing.T, docs ports.Docs, index ports.Index, subject string, p float64, when time.Time) string {
	t.Helper()
	path, err := app.RecordJudgement(context.Background(), docs, index, subject, rainQuestion(), forecast(subject, p, when))
	if err != nil {
		t.Fatalf("record judgement: %v", err)
	}
	return path
}

func row(t *testing.T, index ports.Index, subject, path string) ports.Record {
	t.Helper()
	rows, err := index.Find(context.Background(), ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": subject}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var found []ports.Record
	for _, r := range rows {
		if r.Path == path {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("rows for %s = %d, want exactly 1", path, len(found))
	}
	return found[0]
}

func TestAttachingAnOutcomeIsANewRevisionOfTheSameDocument(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	original, _ := docs.Get(ctx, path)

	rev, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rev == "" {
		t.Error("no revision returned")
	}

	body, _ := docs.Get(ctx, path)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document is not json: %v", err)
	}
	outcome, ok := doc["outcome"].(map[string]any)
	if !ok || outcome["state"] != "wet" || outcome["when"] != "2026-09-29T09:00:00.000Z" || outcome["note"] != "umbrella needed" {
		t.Errorf("outcome = %v", doc["outcome"])
	}

	history, err := docs.History(ctx, path)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d revisions, err %v; want the judgement then the attachment", len(history), err)
	}
	first, err := docs.GetAt(ctx, path, history[1].Rev)
	if err != nil || string(first) != string(original) {
		t.Errorf("the original judgement is not retrievable unchanged: %v", err)
	}

	r := row(t, index, "harbour-fete", path)
	if r.Fields["outcome"] != "wet" {
		t.Errorf("index outcome = %q, want wet", r.Fields["outcome"])
	}
	if !r.When.Equal(day) {
		t.Errorf("row When = %v, want the judgement's own time %v", r.When, day)
	}
}

func TestACorrectionIsAThirdRevisionAndTheLatestWins(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "fog", When: day}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, wet()); err != nil {
		t.Fatal(err)
	}
	history, _ := docs.History(ctx, path)
	if len(history) != 3 {
		t.Fatalf("history = %d revisions, want 3", len(history))
	}
	earlier, _ := docs.GetAt(ctx, path, history[1].Rev)
	if !strings.Contains(string(earlier), `"state": "fog"`) {
		t.Errorf("the first attachment is not retrievable: %s", earlier)
	}
	if r := row(t, index, "harbour-fete", path); r.Fields["outcome"] != "wet" {
		t.Errorf("index outcome = %q, want the latest, wet", r.Fields["outcome"])
	}
}

func TestAttachingTheSameOutcomeTwiceChangesNothing(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	first, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.AttachOutcome(ctx, docs, index, path, wet())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("revisions %q then %q; a byte-identical attach must be a no-op", first, second)
	}
	if history, _ := docs.History(ctx, path); len(history) != 2 {
		t.Errorf("history = %d revisions, want 2", len(history))
	}
}

func TestAttachingKeepsEveryOtherKeyAndFieldAsItWas(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)

	// A later increment may add document keys and index fields this code
	// does not know. Simulate one: rewrite the document with an extra key,
	// and the row with an extra field.
	body, _ := docs.Get(ctx, path)
	extended := strings.Replace(string(body), "\n}", ",\n  \"fingerprint\": \"abc123\"\n}", 1)
	if _, err := docs.Put(ctx, path, []byte(extended), "extend"); err != nil {
		t.Fatal(err)
	}
	r := row(t, index, "harbour-fete", path)
	r.Fields["verdict"] = "yes"
	if err := index.Upsert(ctx, r); err != nil {
		t.Fatal(err)
	}

	if _, err := app.AttachOutcome(ctx, docs, index, path, wet()); err != nil {
		t.Fatal(err)
	}
	after, _ := docs.Get(ctx, path)
	nullOutcome := strings.Replace(string(after), `"outcome": {
    "state": "wet",
    "when": "2026-09-29T09:00:00.000Z",
    "note": "umbrella needed"
  }`, `"outcome": null`, 1)
	if nullOutcome != extended {
		t.Errorf("attaching changed more than the outcome value:\nbefore:\n%s\nafter:\n%s", extended, after)
	}
	got := row(t, index, "harbour-fete", path)
	if got.Fields["verdict"] != "yes" || got.Fields["model"] != "a-model" || got.Fields["outcome"] != "wet" {
		t.Errorf("row fields = %v; every field but outcome must survive", got.Fields)
	}
}

func TestAttachingRefusesWhatIsNotAJudgement(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	path := judged(t, docs, index, "harbour-fete", 0.7, day)
	if _, err := docs.Put(ctx, "decisions/harbour-fete/x.json", []byte(`{"outcome": null}`), "a decision"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		o    app.Outcome
	}{
		"a decision path":  {"decisions/harbour-fete/x.json", wet()},
		"escaping path":    {"judgements/../decisions/harbour-fete/x.json", wet()},
		"no such document": {"judgements/nobody/2026.json", wet()},
		"empty state":      {path, app.Outcome{State: "  ", When: day}},
		"zero when":        {path, app.Outcome{State: "wet"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.AttachOutcome(ctx, docs, index, tc.path, tc.o); err == nil || !strings.HasPrefix(err.Error(), "outcome: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
	if history, _ := docs.History(ctx, path); len(history) != 1 {
		t.Errorf("a refused attach wrote to the judgement: %d revisions", len(history))
	}
}

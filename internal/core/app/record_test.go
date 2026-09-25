package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func aDocument() app.Document {
	return app.Document{
		Path:    "logbook/day-1.json",
		Body:    []byte(`{"wind":"west"}`),
		Message: "Log day one",
		Kind:    "logbook",
		Fields:  map[string]string{"day": "1"},
		When:    time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	}
}

func TestRecordDocumentWritesTheBodyAndOneIndexRow(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	rev, err := app.RecordDocument(context.Background(), docs, index, aDocument())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if string(docs.put["logbook/day-1.json"]) != `{"wind":"west"}` {
		t.Errorf("body = %q", docs.put["logbook/day-1.json"])
	}
	if len(index.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(index.rows))
	}
	row := index.rows[0]
	if row.Kind != "logbook" || row.Fields["day"] != "1" || row.Rev != rev || row.Path != "logbook/day-1.json" {
		t.Errorf("row = %+v, rev = %q", row, rev)
	}
}

func TestRecordDocumentRejectsAMissingPathOrKind(t *testing.T) {
	for _, mutate := range []func(*app.Document){
		func(d *app.Document) { d.Path = "" },
		func(d *app.Document) { d.Kind = "" },
	} {
		d := aDocument()
		mutate(&d)
		docs, index := newFakeDocs(), &fakeIndex{}
		if _, err := app.RecordDocument(context.Background(), docs, index, d); err == nil {
			t.Errorf("document %+v was recorded", d)
		}
		if len(docs.put) != 0 {
			t.Errorf("a rejected document was written anyway")
		}
	}
}

func TestRecordDocumentRejectsANonCanonicalPath(t *testing.T) {
	d := aDocument()
	d.Path = "decisions/../profile/x.json"
	docs, index := newFakeDocs(), &fakeIndex{}
	if _, err := app.RecordDocument(context.Background(), docs, index, d); err == nil {
		t.Fatal("a non-canonical path was recorded")
	}
	if len(docs.put) != 0 {
		t.Errorf("a rejected path was written anyway")
	}
}

func TestRecordDocumentReportsAFailedUpsertWithTheRevision(t *testing.T) {
	docs := newFakeDocs()
	rev, err := app.RecordDocument(context.Background(), docs, &failingIndex{err: errors.New("index down")}, aDocument())
	if err == nil {
		t.Fatal("a failed upsert was not reported")
	}
	if rev == "" {
		t.Error("the document was committed but no revision was returned")
	}
	if !strings.Contains(err.Error(), "logbook/day-1.json") {
		t.Errorf("error does not name the path: %v", err)
	}
}

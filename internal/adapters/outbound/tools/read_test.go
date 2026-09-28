package tools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
)

func TestIndexFindReturnsMatchingRows(t *testing.T) {
	docs, index := store(t)
	put := tools.NewDocsPut(docs, index)
	for _, day := range []string{"1", "2"} {
		if _, err := put.Invoke(context.Background(), map[string]string{
			"path": "logbook/day-" + day + ".json", "body": `{"wind":"west"}`, "kind": "logbook",
			"fields": "day: \"" + day + "\"\nport: Dover",
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := tools.NewIndexFind(index).Invoke(context.Background(), map[string]string{
		"kind": "logbook", "match": "day: \"2\"",
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := out.(map[string]any)["rows"].([]map[string]any)
	if len(rows) != 1 || rows[0]["path"] != "logbook/day-2.json" || rows[0]["fields"].(map[string]string)["port"] != "Dover" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestIndexFindReturnsNewestFirst(t *testing.T) {
	docs, index := store(t)
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for i, day := range []string{"old", "new"} {
		if _, err := app.RecordDocument(context.Background(), docs, index, app.Document{
			Path: "logbook/" + day + ".json", Body: []byte(`{}`), Message: "log " + day,
			Kind: "logbook", Fields: map[string]string{"port": "Dover"}, When: base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	find := tools.NewIndexFind(index)
	for _, limit := range []string{"", "1"} {
		out, err := find.Invoke(context.Background(), map[string]string{"kind": "logbook", "limit": limit})
		if err != nil {
			t.Fatal(err)
		}
		rows := out.(map[string]any)["rows"].([]map[string]any)
		if len(rows) == 0 || rows[0]["path"] != "logbook/new.json" {
			t.Errorf("limit %q: rows = %+v; want logbook/new.json first", limit, rows)
		}
		if limit == "1" && len(rows) != 1 {
			t.Errorf("limit 1: %d rows", len(rows))
		}
	}
}

func TestIndexFindRefusesAMissingKindAndABadLimit(t *testing.T) {
	_, index := store(t)
	find := tools.NewIndexFind(index)
	for name, with := range map[string]map[string]string{
		"no kind":        {"match": "a: b"},
		"limit not int":  {"kind": "logbook", "limit": "many"},
		"limit not >0":   {"kind": "logbook", "limit": "0"},
		"match not flat": {"kind": "logbook", "match": "a: [1, 2]"},
	} {
		if _, err := find.Invoke(context.Background(), with); err == nil || !strings.HasPrefix(err.Error(), "index.find: ") {
			t.Errorf("%s: err = %v, want an index.find error", name, err)
		}
	}
}

func TestDocsGetReturnsTextAndParsedJSON(t *testing.T) {
	docs, index := store(t)
	if _, err := tools.NewDocsPut(docs, index).Invoke(context.Background(), map[string]string{
		"path": "logbook/day-1.json", "body": `{"wind":"west"}`, "kind": "logbook",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := tools.NewDocsGet(docs, 1<<20).Invoke(context.Background(), map[string]string{"path": "logbook/day-1.json"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["text"] != `{"wind":"west"}` || m["doc"].(map[string]any)["wind"] != "west" {
		t.Errorf("out = %+v", m)
	}
}

func TestDocsGetReadsARevisionAndBoundsTheBody(t *testing.T) {
	docs, index := store(t)
	put := tools.NewDocsPut(docs, index)
	with := map[string]string{"path": "notes/a.txt", "body": "first", "kind": "note"}
	first, err := put.Invoke(context.Background(), with)
	if err != nil {
		t.Fatal(err)
	}
	with["body"] = "second, and longer"
	if _, err := put.Invoke(context.Background(), with); err != nil {
		t.Fatal(err)
	}
	get := tools.NewDocsGet(docs, 8)
	out, err := get.Invoke(context.Background(), map[string]string{"path": "notes/a.txt", "rev": first.(map[string]any)["rev"].(string)})
	if err != nil || out.(map[string]any)["text"] != "first" {
		t.Errorf("at first rev: out = %v, err = %v", out, err)
	}
	if _, err := get.Invoke(context.Background(), map[string]string{"path": "notes/a.txt"}); err == nil {
		t.Error("an 18-byte body under an 8-byte bound was returned")
	}
	if _, ok := out.(map[string]any)["doc"]; ok {
		t.Error("plain text was given a parsed doc")
	}
}

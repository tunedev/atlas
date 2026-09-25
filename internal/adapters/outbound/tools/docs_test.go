package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestDocsPutCommitsAndIndexesTheBody(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewDocsPut(docs, index).Invoke(context.Background(), map[string]string{
		"path": "logbook/day-1.json", "body": `{"wind":"west"}`, "kind": "logbook",
		"fields": "day: \"1\"\nport: Dover", "message": "Log day one", "expect": "json",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := out.(map[string]any)
	if m["path"] != "logbook/day-1.json" || m["rev"] == "" {
		t.Errorf("out = %v", m)
	}
	body, err := docs.Get(context.Background(), "logbook/day-1.json")
	if err != nil || string(body) != `{"wind":"west"}` {
		t.Fatalf("committed body = %q, err = %v", body, err)
	}
	rows, err := index.Find(context.Background(), ports.Query{Kind: "logbook", Match: map[string]string{"port": "Dover"}, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Fields["day"] != "1" {
		t.Errorf("rows = %+v, err = %v", rows, err)
	}
}

func TestDocsPutACorrectionIsANewRevisionAndTheOriginalSurvives(t *testing.T) {
	docs, index := store(t)
	put := tools.NewDocsPut(docs, index)
	with := map[string]string{"path": "logbook/day-1.json", "body": `{"wind":"west"}`, "kind": "logbook"}
	if _, err := put.Invoke(context.Background(), with); err != nil {
		t.Fatalf("first: %v", err)
	}
	with["body"] = `{"wind":"east"}`
	if _, err := put.Invoke(context.Background(), with); err != nil {
		t.Fatalf("second: %v", err)
	}
	history, err := docs.History(context.Background(), "logbook/day-1.json")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %+v, err = %v", history, err)
	}
	original, err := docs.GetAt(context.Background(), "logbook/day-1.json", history[1].Rev)
	if err != nil || string(original) != `{"wind":"west"}` {
		t.Errorf("original = %q, err = %v", original, err)
	}
}

func TestDocsPutWithExpectJSONRefusesMalformedJSONAndCommitsNothing(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewDocsPut(docs, index).Invoke(context.Background(), map[string]string{
		"path": "logbook/day-1.json", "body": `{"wind":"west",}`, "kind": "logbook", "expect": "json",
	})
	if err == nil {
		t.Fatal("a body with a trailing comma was committed")
	}
	if paths, _ := docs.List(context.Background(), ""); len(paths) != 0 {
		t.Errorf("something was committed anyway: %v", paths)
	}
}

func TestDocsPutRejectsBadInput(t *testing.T) {
	docs, index := store(t)
	for name, with := range map[string]map[string]string{
		"no path":        {"body": "x", "kind": "k"},
		"no kind":        {"path": "a.txt", "body": "x"},
		"unknown expect": {"path": "a.txt", "body": "x", "kind": "k", "expect": "xml"},
		"bad fields":     {"path": "a.txt", "body": "x", "kind": "k", "fields": "[unclosed"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tools.NewDocsPut(docs, index).Invoke(context.Background(), with)
			if err == nil || !strings.HasPrefix(err.Error(), "docs.put: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

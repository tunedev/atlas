package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/duckindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// TestDeletingDuckindexAndRebuildingHistoryLosesNoRevision is RebuildHistory's
// counterpart to TestDeletingTheIndexAndRebuildingLosesNothing: it proves
// the same property for a revision-keyed index, against the real gitdocs
// store and the real duckindex database. Three revisions of one path, each
// with a distinct field value, must all survive deleting the index file and
// rebuilding from git alone.
func TestDeletingDuckindexAndRebuildingHistoryLosesNoRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "history.duckdb")

	docs, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	states := []string{"open", "in-progress", "closed"}
	for _, state := range states {
		if _, err := docs.Put(ctx, "items/one.md", []byte("state: "+state+"\n"), "update"); err != nil {
			t.Fatalf("put %s: %v", state, err)
		}
	}

	idx, err := duckindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := app.RebuildHistory(ctx, docs, idx, extractItem); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find before: %v", err)
	}
	if len(before) != len(states) {
		t.Fatalf("rebuild indexed %d revisions, want %d; the test cannot discriminate", len(before), len(states))
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Destroy the index completely, leaving only the record.
	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("remove index: %v", err)
	}

	rebuilt, err := duckindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen index: %v", err)
	}
	defer rebuilt.Close()
	if err := app.RebuildHistory(ctx, docs, rebuilt, extractItem); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	after, err := rebuilt.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("rebuilt index holds %d revisions, original held %d", len(after), len(before))
	}

	gotStates := map[string]bool{}
	revsSeen := map[ports.Revision]bool{}
	for _, r := range after {
		if r.Path != "items/one.md" {
			t.Errorf("unexpected path %q in results", r.Path)
		}
		if r.Rev == "" {
			t.Error("record has empty Rev; RebuildHistory did not set it from the revision's DocMeta")
		}
		revsSeen[r.Rev] = true
		gotStates[r.Fields["state"]] = true
	}
	if len(revsSeen) != len(states) {
		t.Errorf("got %d distinct revisions, want %d; a revision-keyed index must keep one row per revision", len(revsSeen), len(states))
	}
	for _, want := range states {
		if !gotStates[want] {
			t.Errorf("revision with state %q missing after rebuild", want)
		}
	}
}

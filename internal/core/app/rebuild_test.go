package app_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// byPath sorts records by Path. ports.Index.Find makes no ordering promise,
// so comparing two Find results positionally requires sorting them first.
func byPath(records []ports.Record) {
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
}

// extractItem is a use-case-neutral extractor used only to exercise Rebuild:
// it indexes any path under items/ with Kind "item", reading a "state:
// <value>" line from the body. Anything else, including a path under
// items/ with no state line, is not indexed.
func extractItem(path string, body []byte) (ports.Record, bool) {
	if !strings.HasPrefix(path, "items/") {
		return ports.Record{}, false
	}
	state, ok := findStateLine(body)
	if !ok {
		return ports.Record{}, false
	}
	return ports.Record{
		Path:   path,
		Kind:   "item",
		Fields: map[string]string{"state": state},
	}, true
}

func findStateLine(body []byte) (string, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, "state: "); ok {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

func TestDeletingTheIndexAndRebuildingLosesNothing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	docs, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	for _, p := range []string{"items/one.md", "items/two.md", "other/three.md"} {
		if _, err := docs.Put(ctx, p, []byte("state: open\n"), "add"); err != nil {
			t.Fatalf("put %s: %v", p, err)
		}
	}

	idx, err := sqlindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := app.Rebuild(ctx, docs, idx, extractItem); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find before: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("rebuild indexed nothing; the test cannot discriminate")
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Destroy the index completely, leaving only the record.
	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("remove index: %v", err)
	}

	rebuilt, err := sqlindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen index: %v", err)
	}
	defer rebuilt.Close()
	if err := app.Rebuild(ctx, docs, rebuilt, extractItem); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	after, err := rebuilt.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find after: %v", err)
	}

	if len(after) != len(before) {
		t.Fatalf("rebuilt index holds %d records, original held %d", len(after), len(before))
	}
	byPath(before)
	byPath(after)
	for i := range before {
		if after[i].Path != before[i].Path || after[i].Fields["state"] != before[i].Fields["state"] {
			t.Errorf("record %d differs: before %+v, after %+v", i, before[i], after[i])
		}
	}
}

// TestRebuildResetsBeforeIndexing pins the ordering the happy-path test
// cannot: Rebuild must clear the index before re-deriving it. Seeding a
// record whose path is absent from the record store and asserting it is
// gone afterwards is the only way to fail an implementation that appends
// instead of resetting, since an append-only rebuild would still pass the
// happy-path test above.
func TestRebuildResetsBeforeIndexing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	docs, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	if _, err := docs.Put(ctx, "items/one.md", []byte("state: open\n"), "add"); err != nil {
		t.Fatalf("put: %v", err)
	}

	idx, err := sqlindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	stale := ports.Record{
		Path:   "items/stale.md",
		Rev:    "stale-rev",
		Kind:   "item",
		Fields: map[string]string{"state": "open"},
		When:   time.Now(),
	}
	if err := idx.Upsert(ctx, stale); err != nil {
		t.Fatalf("seed stale record: %v", err)
	}

	if err := app.Rebuild(ctx, docs, idx, extractItem); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	records, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	for _, r := range records {
		if r.Path == "items/stale.md" {
			t.Fatal("stale record survived rebuild; Reset was not called first")
		}
	}
}

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

// extractItem is a use-case-neutral extractor used only to exercise Rebuild
// and RebuildHistory: it indexes any path under items/ with Kind "item",
// reading a "state: <value>" line from the body. Anything else, including a
// path under items/ with no state line, is not indexed. It never sets Rev or
// When itself: both walks stamp those from Docs after extract runs, so a
// body carrying its own rev/when text would prove nothing about whether the
// walk actually stamps them.
func extractItem(path string, body []byte) (ports.Record, bool) {
	if !strings.HasPrefix(path, "items/") {
		return ports.Record{}, false
	}
	state, ok := findLine(body, "state: ")
	if !ok {
		return ports.Record{}, false
	}
	return ports.Record{
		Path:   path,
		Kind:   "item",
		Fields: map[string]string{"state": state},
	}, true
}

func findLine(body []byte, prefix string) (string, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// wantItemCount is items/one.md and items/two.md. other/three.md must be
// excluded by extractItem's own rejection, not merely absent by coincidence.
const wantItemCount = 2

// assertOnlyExpectedItemsIndexed checks the Kind:"item" result directly
// against the paths that should be present, and separately checks the index
// for records extract rejected: an implementation that ignores extract's ok
// return and upserts a zero-value Record anyway would produce rows with an
// empty Kind, which a Kind:"item" query alone would never surface.
func assertOnlyExpectedItemsIndexed(t *testing.T, idx ports.Index, records []ports.Record) {
	t.Helper()
	if len(records) != wantItemCount {
		t.Fatalf("rebuild indexed %d items, want %d", len(records), wantItemCount)
	}
	for _, r := range records {
		if strings.HasPrefix(r.Path, "other/") {
			t.Errorf("record %+v has path under other/; extract's rejection was not honoured", r)
		}
	}
	rejected, err := idx.Find(context.Background(), ports.Query{Kind: ""})
	if err != nil {
		t.Fatalf("find records with empty kind: %v", err)
	}
	if len(rejected) != 0 {
		t.Errorf("found %d records with empty Kind; a rejected path was indexed anyway", len(rejected))
	}
}

func TestDeletingTheIndexAndRebuildingLosesNothing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	docs, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	bodies := map[string][]byte{
		"items/one.md":   []byte("state: open\n"),
		"items/two.md":   []byte("state: closed\n"),
		"other/three.md": []byte("state: open\n"),
	}
	// Git commit timestamps carry only whole-second precision, so the window
	// gives each side a second of slack rather than comparing to the
	// sub-second time.Now() taken here.
	start := time.Now().Add(-time.Second)
	wantRev := map[string]ports.Revision{}
	for _, p := range []string{"items/one.md", "items/two.md", "other/three.md"} {
		rev, err := docs.Put(ctx, p, bodies[p], "add")
		if err != nil {
			t.Fatalf("put %s: %v", p, err)
		}
		wantRev[p] = rev
	}
	end := time.Now().Add(time.Second)

	idx, err := sqlindex.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := app.Rebuild(ctx, docs, idx, extractItem); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find before: %v", err)
	}
	assertOnlyExpectedItemsIndexed(t, idx, before)
	for _, r := range before {
		if r.Rev != wantRev[r.Path] {
			t.Errorf("record %+v has Rev %q, want %q as returned by Put", r, r.Rev, wantRev[r.Path])
		}
		if r.When.Before(start) || r.When.After(end) {
			t.Errorf("record %+v has When %v outside the put window [%v, %v]; Rebuild did not stamp it from History", r, r.When, start, end)
		}
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
	assertOnlyExpectedItemsIndexed(t, rebuilt, after)

	if len(after) != len(before) {
		t.Fatalf("rebuilt index holds %d records, original held %d", len(after), len(before))
	}
	byPath(before)
	byPath(after)
	for i := range before {
		if after[i].Path != before[i].Path ||
			after[i].Fields["state"] != before[i].Fields["state"] ||
			after[i].Rev != before[i].Rev ||
			!after[i].When.Equal(before[i].When) {
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

package sqlindex_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/core/ports"
)

func open(t *testing.T) *sqlindex.Index {
	t.Helper()
	idx, err := sqlindex.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func TestARecordIsFoundByField(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	rec := ports.Record{
		Path:   "items/one.md",
		Rev:    "r1",
		Kind:   "item",
		Fields: map[string]string{"state": "open", "owner": "alpha"},
		When:   time.Now(),
	}
	if err := idx.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item", Match: map[string]string{"state": "open"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 || got[0].Path != "items/one.md" {
		t.Fatalf("find returned %v", got)
	}
}

func TestUpsertReplacesRatherThanDuplicates(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	for _, state := range []string{"open", "closed"} {
		err := idx.Upsert(ctx, ports.Record{
			Path: "items/one.md", Rev: "r", Kind: "item",
			Fields: map[string]string{"state": state}, When: time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", state, err)
		}
	}

	all, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("found %d records for one path, want 1", len(all))
	}
	if all[0].Fields["state"] != "closed" {
		t.Fatalf("state = %q, want closed", all[0].Fields["state"])
	}
}

func TestAQueryThatMatchesNothingIsNotAnError(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	got, err := idx.Find(ctx, ports.Query{Kind: "item", Match: map[string]string{"state": "absent"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("found %d records, want none", len(got))
	}
}

func TestResetEmptiesTheIndex(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	if err := idx.Upsert(ctx, ports.Record{Path: "a", Rev: "r", Kind: "item", When: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := idx.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("index still holds %d records after Reset", len(got))
	}
}

func TestAMatchValueContainingSQLIsTreatedAsData(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	if err := idx.Upsert(ctx, ports.Record{
		Path: "a", Rev: "r", Kind: "item",
		Fields: map[string]string{"state": "open"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := idx.Find(ctx, ports.Query{
		Kind:  "item",
		Match: map[string]string{"state": "' OR '1'='1"},
	})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a quoted match value matched %d records; it is being interpolated, not bound", len(got))
	}
}

// TestFindReturnsOnlyRecordsMatchingTheQuery guards Match filtering with the
// one fixture shape the earlier tests lack: two records of the same Kind
// that disagree on the matched field. With a single candidate record, an
// unfiltered query happens to return the same answer as a filtered one;
// with two, only a real filter tells them apart.
func TestFindReturnsOnlyRecordsMatchingTheQuery(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/one.md", Rev: "r", Kind: "item",
		Fields: map[string]string{"state": "open"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert one: %v", err)
	}
	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/two.md", Rev: "r", Kind: "item",
		Fields: map[string]string{"state": "closed"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert two: %v", err)
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item", Match: map[string]string{"state": "open"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 || got[0].Path != "items/one.md" {
		t.Fatalf("find returned %v, want only items/one.md", got)
	}
}

// TestWhenSurvivesARoundTrip guards the corpus timeline: when_utc is stored
// as an INTEGER, and a timezone or precision bug there would pass every
// other test in this file while corrupting every timestamp in the index.
// Equality is checked with time.Time.Equal, which compares the instant in
// time and ignores monotonic reading and location — the index stores
// nanoseconds since the Unix epoch in UTC, so the instant compared is exact,
// but the monotonic clock reading that time.Now() attaches is necessarily
// lost across a database round trip and is not asserted here.
func TestWhenSurvivesARoundTrip(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	when := time.Date(2026, 9, 19, 12, 34, 56, 789000000, time.FixedZone("UTC+2", 2*60*60))
	if err := idx.Upsert(ctx, ports.Record{Path: "a", Rev: "r", Kind: "item", When: when}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d records, want 1", len(got))
	}
	if !got[0].When.Equal(when) {
		t.Fatalf("When = %v, want %v", got[0].When, when)
	}
}

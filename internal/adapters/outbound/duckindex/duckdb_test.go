package duckindex_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/duckindex"
	"github.com/tunedev/atlas/internal/core/ports"
)

func open(t *testing.T) *duckindex.Index {
	t.Helper()
	idx, err := duckindex.Open(context.Background(), filepath.Join(t.TempDir(), "index.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// TestEveryRevisionIsKeptRatherThanReplaced is the test that distinguishes
// this index from sqlindex: the primary key is (path, rev), so three
// revisions of one path produce three rows, not one.
func TestEveryRevisionIsKeptRatherThanReplaced(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	for _, rev := range []string{"r1", "r2", "r3"} {
		err := idx.Upsert(ctx, ports.Record{
			Path: "items/one.md", Rev: ports.Revision(rev), Kind: "item",
			Fields: map[string]string{"state": "open"}, When: time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", rev, err)
		}
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d rows for three revisions of one path, want 3; this index must keep history", len(got))
	}
}

// TestARecordIsFoundByField upserts two records that disagree on the matched
// field. A single-candidate fixture would pass even if Match filtering were
// deleted entirely; two records with different states requires the filter to
// actually run.
func TestARecordIsFoundByField(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/one.md", Rev: "r1", Kind: "item",
		Fields: map[string]string{"state": "open"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert one: %v", err)
	}
	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/two.md", Rev: "r1", Kind: "item",
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

// TestMatchIsScopedToTheRevisionNotTheWholePath guards the tuple subquery
// in Find: fields belong to one revision, not one path, so a path with two
// revisions whose field values disagree must not have those revisions
// cross-contaminate each other's match. Degrading the (path, rev) tuple
// subquery to a path-only one (sqlindex's shape) would let a stale
// revision's field value satisfy a query meant for the current one.
func TestMatchIsScopedToTheRevisionNotTheWholePath(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/one.md", Rev: "r1", Kind: "item",
		Fields: map[string]string{"state": "open"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert r1: %v", err)
	}
	if err := idx.Upsert(ctx, ports.Record{
		Path: "items/one.md", Rev: "r2", Kind: "item",
		Fields: map[string]string{"state": "closed"}, When: time.Now(),
	}); err != nil {
		t.Fatalf("upsert r2: %v", err)
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item", Match: map[string]string{"state": "open"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 || got[0].Rev != "r1" {
		t.Fatalf("find returned %v, want exactly the r1 revision", got)
	}
}

// TestUpsertReplacesTheSameRevision asserts the primary key is (path, rev),
// not path: upserting the same path and same rev twice replaces the one row
// rather than erroring or duplicating it.
func TestUpsertReplacesTheSameRevision(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	for _, state := range []string{"open", "closed"} {
		err := idx.Upsert(ctx, ports.Record{
			Path: "items/one.md", Rev: "r1", Kind: "item",
			Fields: map[string]string{"state": state}, When: time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", state, err)
		}
	}

	got, err := idx.Find(ctx, ports.Query{Kind: "item"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d rows for one path and rev upserted twice, want 1", len(got))
	}
	if got[0].Fields["state"] != "closed" {
		t.Fatalf("state = %q, want closed", got[0].Fields["state"])
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

	if err := idx.Upsert(ctx, ports.Record{Path: "a", Rev: "r1", Kind: "item", When: time.Now()}); err != nil {
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
		Path: "a", Rev: "r1", Kind: "item",
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

// TestWhenSurvivesARoundTrip guards when_utc's BIGINT storage: a non-UTC
// zone and a nonzero nanosecond component each fail this independently if
// the timezone is lost or the value is truncated to seconds. Equality is
// checked with time.Time.Equal rather than ==, since == also compares the
// monotonic reading and *Location, neither of which a database round trip
// preserves.
func TestWhenSurvivesARoundTrip(t *testing.T) {
	ctx := context.Background()
	idx := open(t)

	when := time.Date(2026, 9, 19, 12, 34, 56, 789000000, time.FixedZone("UTC+2", 2*60*60))
	if err := idx.Upsert(ctx, ports.Record{Path: "a", Rev: "r1", Kind: "item", When: when}); err != nil {
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

package gitdocs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestAPriorRevisionIsStillReadable(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	first, err := store.Put(ctx, "a.md", []byte("one"), "first")
	if err != nil {
		t.Fatalf("put first: %v", err)
	}
	if _, err := store.Put(ctx, "a.md", []byte("two"), "second"); err != nil {
		t.Fatalf("put second: %v", err)
	}

	latest, err := store.Get(ctx, "a.md")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(latest) != "two" {
		t.Fatalf("latest = %q, want two", latest)
	}

	old, err := store.GetAt(ctx, "a.md", first)
	if err != nil {
		t.Fatalf("get at first: %v", err)
	}
	if string(old) != "one" {
		t.Fatalf("first revision = %q, want one", old)
	}
}

func TestHistoryListsRevisionsNewestFirst(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, m := range []string{"first", "second", "third"} {
		if _, err := store.Put(ctx, "a.md", []byte(m), m); err != nil {
			t.Fatalf("put %s: %v", m, err)
		}
	}

	got, err := store.History(ctx, "a.md")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("history returned %d revisions, want 3", len(got))
	}
	if got[0].Message != "third" {
		t.Fatalf("newest message = %q, want third", got[0].Message)
	}
	if got[0].Path != "a.md" {
		t.Fatalf("path = %q, want a.md", got[0].Path)
	}
}

func TestHistoryEntryRevisionCanBeReadWithGetAt(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, body := range []string{"first", "second", "third"} {
		if _, err := store.Put(ctx, "a.md", []byte(body), body); err != nil {
			t.Fatalf("put %s: %v", body, err)
		}
	}

	got, err := store.History(ctx, "a.md")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("history returned %d revisions, want 3", len(got))
	}
	for _, entry := range got {
		if entry.When.IsZero() {
			t.Fatalf("revision %q has a zero When", entry.Rev)
		}
	}

	oldest := got[len(got)-1]
	body, err := store.GetAt(ctx, "a.md", oldest.Rev)
	if err != nil {
		t.Fatalf("get at oldest history revision: %v", err)
	}
	if string(body) != "first" {
		t.Fatalf("body at oldest history revision = %q, want first", body)
	}
}

func TestGetAtAnUnknownRevisionIsAnError(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Put(ctx, "a.md", []byte("one"), "first"); err != nil {
		t.Fatalf("put: %v", err)
	}

	bogus := ports.Revision(strings.Repeat("0", 40))
	if _, err := store.GetAt(ctx, "a.md", bogus); err == nil {
		t.Fatal("get at an unknown revision returned no error")
	} else if !strings.Contains(err.Error(), "a.md") || !strings.Contains(err.Error(), string(bogus)) {
		t.Fatalf("error %q does not name both path and revision", err)
	}
}

func TestGetAtAPathAbsentFromThatRevisionIsAnError(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rev, err := store.Put(ctx, "a.md", []byte("one"), "first")
	if err != nil {
		t.Fatalf("put a: %v", err)
	}
	if _, err := store.Put(ctx, "b.md", []byte("two"), "second"); err != nil {
		t.Fatalf("put b: %v", err)
	}

	if _, err := store.GetAt(ctx, "b.md", rev); err == nil {
		t.Fatal("get at a path absent from that revision returned no error")
	} else if !strings.Contains(err.Error(), "b.md") || !strings.Contains(err.Error(), string(rev)) {
		t.Fatalf("error %q does not name both path and revision", err)
	}
}

func TestHistoryOfOnePathIgnoresOtherPaths(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Put(ctx, "a.md", []byte("a"), "touch a"); err != nil {
		t.Fatalf("put a: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.Put(ctx, "b.md", []byte("b"), "touch b"); err != nil {
			t.Fatalf("put b: %v", err)
		}
	}

	got, err := store.History(ctx, "a.md")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("history of a.md returned %d revisions, want 1; it is following other paths", len(got))
	}
}

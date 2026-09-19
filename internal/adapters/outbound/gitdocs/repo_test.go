package gitdocs_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
)

// chmodTree sets mode on every entry under root, so a whole .git directory
// can be made unwritable to force a failure after Put has already written
// the working-tree file.
func chmodTree(t *testing.T, root string, mode os.FileMode) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatalf("chmod %s to %o: %v", root, mode, err)
	}
}

func TestPutThenGetReturnsTheBody(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := store.Put(ctx, "notes/one.md", []byte("first"), "add one"); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get(ctx, "notes/one.md")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "first" {
		t.Fatalf("body = %q, want first", got)
	}
}

func TestOpenInitialisesAnEmptyDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	if _, err := gitdocs.Open(ctx, root); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := gitdocs.Open(ctx, root); err != nil {
		t.Fatalf("second open on an existing repo: %v", err)
	}
	if _, err := filepath.Abs(root); err != nil {
		t.Fatalf("abs: %v", err)
	}
}

func TestGetAnAbsentPathIsAnError(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Get(ctx, "nothing/here.md"); err == nil {
		t.Fatal("reading an absent path returned no error")
	}
}

func TestAPathCannotEscapeTheRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Both relative escapes clean down to the same target once joined onto
	// root: a file named escape.md next to root, one directory up. An
	// absolute path is caught before any relative resolution, so it has no
	// such target to check.
	escapeTarget := filepath.Join(filepath.Dir(root), "escape.md")
	cases := []struct {
		path   string
		target string
	}{
		{"../escape.md", escapeTarget},
		{"a/../../escape.md", escapeTarget},
		{"/etc/passwd", ""},
	}

	for _, c := range cases {
		if _, err := store.Put(ctx, c.path, []byte("x"), "escape"); err == nil {
			t.Errorf("path %q was accepted", c.path)
		}
		if c.target == "" {
			continue
		}
		if _, statErr := os.Stat(c.target); !os.IsNotExist(statErr) {
			t.Errorf("path %q escaped the root: found file at %q (stat err = %v)", c.path, c.target, statErr)
		}
	}
}

func TestAFailedPutLeavesTheWorktreeUnchanged(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := gitdocs.Open(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Put(ctx, "notes/one.md", []byte("original"), "add one"); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	// Force staging or committing to fail after the working-tree write has
	// already happened: git needs to write a new blob object and update the
	// index, both of which require write access inside .git.
	gitDir := filepath.Join(root, ".git")
	chmodTree(t, gitDir, 0o555)
	t.Cleanup(func() { chmodTree(t, gitDir, 0o755) })

	if _, err := store.Put(ctx, "notes/one.md", []byte("changed"), "change one"); err == nil {
		t.Fatal("put on an existing path succeeded despite the commit being forced to fail")
	}
	if _, err := store.Put(ctx, "notes/two.md", []byte("new"), "add two"); err == nil {
		t.Fatal("put on a new path succeeded despite the commit being forced to fail")
	}

	got, err := store.Get(ctx, "notes/one.md")
	if err != nil {
		t.Fatalf("get existing path after failed put: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("existing path = %q after failed put, want original content restored", got)
	}

	if _, err := store.Get(ctx, "notes/two.md"); err == nil {
		t.Fatal("new path should not exist on disk after its commit failed")
	}
}

func TestListReturnsPathsUnderAPrefix(t *testing.T) {
	ctx := context.Background()
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, p := range []string{"a/one.md", "a/two.md", "b/three.md"} {
		if _, err := store.Put(ctx, p, []byte("x"), "add"); err != nil {
			t.Fatalf("put %s: %v", p, err)
		}
	}
	got, err := store.List(ctx, "a/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list returned %v, want two paths under a/", got)
	}
}

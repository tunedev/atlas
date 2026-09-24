package feedsource_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tunedev/atlas/internal/adapters/outbound/feedsource"
	"github.com/tunedev/atlas/internal/core/ports"
)

var (
	t1 = time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC)
	t2 = t1.Add(6 * time.Hour)
)

// origin is a bare repository standing in for the remote, fed by a working
// repository the test commits into and pushes from.
type origin struct {
	t    *testing.T
	bare string
	work string
	repo *git.Repository
}

func newOrigin(t *testing.T) *origin {
	t.Helper()
	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	repo, err := git.PlainInit(work, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	return &origin{t: t, bare: bare, work: work, repo: repo}
}

// commit writes each file (a nil body deletes it), commits at when, and
// pushes to the bare repository's main.
func (o *origin) commit(when time.Time, files map[string][]byte) plumbing.Hash {
	o.t.Helper()
	wt, err := o.repo.Worktree()
	if err != nil {
		o.t.Fatal(err)
	}
	for name, body := range files {
		if body == nil {
			if _, err := wt.Remove(name); err != nil {
				o.t.Fatal(err)
			}
			continue
		}
		full := filepath.Join(o.work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			o.t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			o.t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			o.t.Fatal(err)
		}
	}
	sig := &object.Signature{Name: "test", Email: "test@example.com", When: when}
	h, err := wt.Commit("update", &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		o.t.Fatal(err)
	}
	o.push()
	return h
}

func (o *origin) tag(name string, h plumbing.Hash) {
	o.t.Helper()
	sig := &object.Signature{Name: "test", Email: "test@example.com", When: t1}
	if _, err := o.repo.CreateTag(name, h, &git.CreateTagOptions{Message: name, Tagger: sig}); err != nil {
		o.t.Fatal(err)
	}
	o.push()
}

func (o *origin) push() {
	o.t.Helper()
	err := o.repo.Push(&git.PushOptions{RefSpecs: []config.RefSpec{
		"refs/heads/master:refs/heads/main", "refs/tags/*:refs/tags/*",
	}})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		o.t.Fatal(err)
	}
}

func source(url, ref, cache string) *feedsource.Source {
	return feedsource.New(feedsource.Config{RemoteURL: url, Ref: ref, CachePath: cache, PullTimeout: 10 * time.Second})
}

func ids(items []ports.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestPullReturnsEveryJSONDocumentAtTheTip(t *testing.T) {
	o := newOrigin(t)
	o.commit(t1, map[string][]byte{
		"items/books/two.json": []byte(`{"n":2}`),
		"items/books/one.json": []byte(`{"n":1}`),
		"notes/readme.md":      []byte("not a document"),
	})
	s := source(o.bare, "refs/heads/main", filepath.Join(t.TempDir(), "cache"))

	items, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got := ids(items); !slices.Equal(got, []string{"items/books/one", "items/books/two"}) {
		t.Errorf("ids = %v", got)
	}
	if string(items[0].Body) != `{"n":1}` || !items[0].When.Equal(t1) {
		t.Errorf("item = %q at %v", items[0].Body, items[0].When)
	}
	last, err := s.LastRefreshed(context.Background())
	if err != nil || !last.Equal(t1) {
		t.Errorf("LastRefreshed = %v, %v; want %v", last, err, t1)
	}
}

func TestPullFollowsTheTipAcrossUpdates(t *testing.T) {
	o := newOrigin(t)
	o.commit(t1, map[string][]byte{"items/one.json": []byte(`1`), "items/two.json": []byte(`2`)})
	s := source(o.bare, "refs/heads/main", filepath.Join(t.TempDir(), "cache"))
	if _, err := s.Pull(context.Background()); err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	o.commit(t2, map[string][]byte{"items/two.json": nil, "items/three.json": []byte(`3`)})

	items, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if got := ids(items); !slices.Equal(got, []string{"items/one", "items/three"}) {
		t.Errorf("ids = %v; a deleted document must disappear", got)
	}
	if last, _ := s.LastRefreshed(context.Background()); !last.Equal(t2) {
		t.Errorf("LastRefreshed = %v, want %v", last, t2)
	}
}

func TestTheCacheHoldsOnlyTheTipCommit(t *testing.T) {
	o := newOrigin(t)
	first := o.commit(t1, map[string][]byte{"items/one.json": []byte(`1`)})
	o.commit(t2, map[string][]byte{"items/two.json": []byte(`2`)})
	cache := filepath.Join(t.TempDir(), "cache")
	if _, err := source(o.bare, "refs/heads/main", cache).Pull(context.Background()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	repo, err := git.PlainOpen(cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitObject(first); err == nil {
		t.Error("the cache holds an older commit; Pull must fetch at depth one")
	}
}

func TestATagPinIsFrozenWhileTheBranchMoves(t *testing.T) {
	o := newOrigin(t)
	o.tag("pre-v2", o.commit(t1, map[string][]byte{"items/one.json": []byte(`1`)}))
	o.commit(t2, map[string][]byte{"items/two.json": []byte(`2`)})
	s := source(o.bare, "refs/tags/pre-v2", filepath.Join(t.TempDir(), "cache"))

	items, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got := ids(items); !slices.Equal(got, []string{"items/one"}) {
		t.Errorf("ids = %v; a tag pin must not see later commits", got)
	}
	if last, _ := s.LastRefreshed(context.Background()); !last.Equal(t1) {
		t.Errorf("LastRefreshed = %v, want the tagged commit's %v", last, t1)
	}
}

func TestPullRefusesACacheThatTracksAnotherRemote(t *testing.T) {
	first, second := newOrigin(t), newOrigin(t)
	first.commit(t1, map[string][]byte{"items/one.json": []byte(`1`)})
	cache := filepath.Join(t.TempDir(), "cache")
	if _, err := source(first.bare, "refs/heads/main", cache).Pull(context.Background()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	_, err := source(second.bare, "refs/heads/main", cache).Pull(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tracks") {
		t.Errorf("err = %v; one remote's cache must never serve another's documents", err)
	}
}

func TestLastRefreshedBeforeAnyPullFails(t *testing.T) {
	s := source("unused", "refs/heads/main", filepath.Join(t.TempDir(), "cache"))
	if _, err := s.LastRefreshed(context.Background()); err == nil {
		t.Error("LastRefreshed invented a time for a feed never pulled")
	}
}

// The adapter only ever reads the remote. This is the mechanical half of the
// one-directional relationship that keeps anything local out of the feed.
func TestSourceNeverPushes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "Push") {
				t.Errorf("%s calls %s; the feed source must never push", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

# Increment 1 — The record — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the harness a durable record — git as the system of record, SQLite and DuckDB as indices derived from it and rebuildable at any time.

**Architecture:** A `Docs` port in the core's vocabulary, implemented over go-git against a repository the app owns at `~/.atlas/workspace`. An `Index` port with two adapters that answer different questions: SQLite over the working tree (what is here now), DuckDB over commit history (what happened). Neither index is authoritative; deleting both and rebuilding from git must lose nothing, and a test asserts it.

**Tech Stack:** Go 1.27, `github.com/go-git/go-git/v5` v5.19.2, `modernc.org/sqlite` v1.59.0 (pure Go), `github.com/marcboeker/go-duckdb/v2` v2.4.3 (cgo).

**Spec:** `docs/specs/2026-09-17-job-hunt-harness-design.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md` (Epic 1, stories 1.1-1.6)

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL or string constant naming a use-case concept outside `packs/`, including test fixtures. `internal/arch/vocabulary_test.go` enforces it.
- No core package imports an adapter or a driver. `internal/core/...` compiles in neither `net/http` nor `crypto/tls`. `internal/arch/arch_test.go` enforces both.
- Ports are written in the core's vocabulary, never an adapter's. **No git type, no `plumbing.Hash`, no `*sql.DB` and no DuckDB type may appear in a port signature.**
- `ctx context.Context` first parameter of every blocking or remote call. Never stored in a struct.
- Every remote call has a timeout. Every response body is bounded.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis. Comments describe current behaviour only — no history, no dates, no ticket references, no narrative about what changed.
- Tests assert behaviour. A test that cannot fail is a defect.
- The increment ends with a note in `docs/notes/`.

## Two decisions recorded before they are questioned

**go-git v5, not v6.** v6 is `v6.0.0-alpha.5`. v5.19.2 is the stable line.

**`marcboeker/go-duckdb/v2` v2.4.3, not `duckdb/duckdb-go`.** The official driver is published only as `v2.20000.0-6.preview`. Revisit when it cuts a stable release; the import path is the only thing that changes.

**The `Docs` port's second implementation is nerve's content-addressed store, not "a plain directory".** A plain directory cannot answer "what did this look like three revisions ago", so it cannot implement this port — naming it would have been an interface tax justified by a lie. Nerve already has `internal/core/cas` and `internal/core/version`; that is the real second implementation.

## What "done" means

```bash
go test ./... -race
go build ./...
```

Plus the property that matters, asserted by test rather than claimed:

> Delete both index files, rebuild from git alone, and every query returns what it returned before.

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/ports/docs.go` | `Docs`, `Revision`, `DocMeta` |
| `internal/core/ports/index.go` | `Index`, `Record`, `Query` |
| `internal/adapters/outbound/gitdocs/repo.go` | `Docs` over go-git; opens or initialises the repo |
| `internal/adapters/outbound/gitdocs/history.go` | revision listing and read-at-revision |
| `internal/adapters/outbound/sqlindex/sqlite.go` | `Index` over the working tree |
| `internal/adapters/outbound/duckindex/duckdb.go` | `Index` over commit history |
| `internal/core/app/rebuild.go` | Rebuild both indices from `Docs` alone |
| `.github/workflows/build.yml` | Per-platform build matrix, because DuckDB needs cgo |
| `docs/notes/2026-09-19-increment-1.md` | The increment note |

---

### Task 1: The `Docs` port and a git-backed store

**Files:**
- Create: `internal/core/ports/docs.go`, `internal/adapters/outbound/gitdocs/repo.go`
- Test: `internal/adapters/outbound/gitdocs/repo_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ports.Revision` (a `string`); `ports.DocMeta{Path string, Rev Revision, When time.Time, Message string}`; `ports.Docs` with `Put(ctx context.Context, path string, body []byte, message string) (Revision, error)`, `Get(ctx context.Context, path string) ([]byte, error)`, `List(ctx context.Context, prefix string) ([]string, error)`; `gitdocs.Open(ctx context.Context, root string) (*gitdocs.Store, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/gitdocs/repo_test.go`:

```go
package gitdocs_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
)

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
	store, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, p := range []string{"../escape.md", "a/../../escape.md", "/etc/passwd"} {
		if _, err := store.Put(ctx, p, []byte("x"), "escape"); err == nil {
			t.Errorf("path %q was accepted", p)
		}
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
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapters/outbound/gitdocs/ -v`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the port**

Create `internal/core/ports/docs.go`:

```go
package ports

import (
	"context"
	"time"
)

// Revision identifies one version of a document. It is opaque to the core:
// only the adapter that produced it knows how to resolve it.
type Revision string

// DocMeta describes one revision of one document.
type DocMeta struct {
	Path    string
	Rev     Revision
	When    time.Time
	Message string
}

// Docs is the system of record. Every write is a new revision; nothing is
// overwritten in place.
type Docs interface {
	Put(ctx context.Context, path string, body []byte, message string) (Revision, error)
	Get(ctx context.Context, path string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}
```

- [ ] **Step 4: Add the dependency**

```bash
cd /home/tunedev/forge/atlas
go get github.com/go-git/go-git/v5@v5.19.2
go mod tidy
```

If `go get` fails for a network reason, STOP and report it.

- [ ] **Step 5: Implement the store**

Create `internal/adapters/outbound/gitdocs/repo.go`. It must:

- `Open(ctx, root)` — `git.PlainOpen(root)`, and on `git.ErrRepositoryNotExists` call `git.PlainInit(root, false)`. Return a `*Store` holding the repo and the root.
- `Put` — reject the path first (see below), write the file under the root creating parent directories, `worktree.Add(path)`, `worktree.Commit(message, ...)` with a fixed author drawn from config later but a literal-free default now, and return the resulting hash as a `ports.Revision`.
- `Get` — read the file from the worktree, wrapping a missing file as an error naming the path.
- `List` — walk the worktree under the prefix, skipping `.git`, returning slash-separated paths relative to the root.

**Path safety is a correctness requirement, not hygiene.** A path must be relative, must stay inside the root once cleaned, and must not be absolute. Refuse rather than clean: a cleaned path and a refused path are indistinguishable to a caller that then writes to the wrong place. Use the same shape as `filestore.Disk` used in an earlier increment if that is still present; otherwise:

```go
func safeRelPath(root, p string) (string, error) {
	if p == "" || filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q must be relative and non-empty", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the root", p)
	}
	return clean, nil
}
```

- [ ] **Step 6: Run it and watch it pass**

Run: `go test ./internal/adapters/outbound/gitdocs/ -v`
Expected: PASS, all five tests.

- [ ] **Step 7: Prove the core stayed clean**

Run: `go test ./internal/arch/ -v`
Expected: PASS. If it fails, a git type reached `internal/core` — fix that, not the guard.

- [ ] **Step 8: Commit**

```bash
git add internal/core/ports/docs.go internal/adapters/outbound/gitdocs go.mod go.sum
git commit -m "Make a document's history the record rather than its latest copy"
```

---

### Task 2: History — list revisions, and read one

**Files:**
- Create: `internal/adapters/outbound/gitdocs/history.go`
- Modify: `internal/core/ports/docs.go`
- Test: `internal/adapters/outbound/gitdocs/history_test.go`

**Interfaces:**
- Consumes: `gitdocs.Open`, `ports.Revision`, `ports.DocMeta`.
- Produces: `Docs` gains `History(ctx context.Context, path string) ([]DocMeta, error)` and `GetAt(ctx context.Context, path string, rev Revision) ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/gitdocs/history_test.go`:

```go
package gitdocs_test

import (
	"context"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
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
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapters/outbound/gitdocs/ -run 'TestAPrior|TestHistory' -v`
Expected: FAIL — `GetAt` and `History` undefined.

- [ ] **Step 3: Extend the port**

Add to `internal/core/ports/docs.go`'s `Docs` interface:

```go
	History(ctx context.Context, path string) ([]DocMeta, error)
	GetAt(ctx context.Context, path string, rev Revision) ([]byte, error)
```

- [ ] **Step 4: Implement**

Create `internal/adapters/outbound/gitdocs/history.go`.

`History` uses `repo.Log(&git.LogOptions{FileName: &path})` and maps each commit to a `ports.DocMeta`. **Note `LogOptions.FileName` takes a `*string`** — passing a loop variable's address is a bug waiting to happen; take the address of a local copy.

`GetAt` resolves the revision to a commit, then `commit.File(path)` and reads its contents. A revision that does not exist, or a path absent at that revision, is an error naming both.

Three details that will bite otherwise:

- The third test asserts `History` filters by path. `git.LogOptions` without `FileName` returns every commit, and that test exists precisely to catch it.
- Newest-first is `Log`'s natural order; do not sort, and do not rely on timestamps, which can tie at one-second resolution when tests commit in a tight loop.
- Commit timestamps in a fast loop can be identical. Never order by `When`.

- [ ] **Step 5: Run it and watch it pass**

Run: `go test ./internal/adapters/outbound/gitdocs/ -v`
Expected: PASS, all eight tests.

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports/docs.go internal/adapters/outbound/gitdocs
git commit -m "Answer what a document looked like, not only what it is"
```

---

### Task 3: The `Index` port and a SQLite index of the working tree

**Files:**
- Create: `internal/core/ports/index.go`, `internal/adapters/outbound/sqlindex/sqlite.go`
- Test: `internal/adapters/outbound/sqlindex/sqlite_test.go`

**Interfaces:**
- Consumes: `ports.Docs`, `ports.DocMeta`.
- Produces: `ports.Record{Path string, Rev Revision, Kind string, Fields map[string]string, When time.Time}`; `ports.Index` with `Upsert(ctx context.Context, r Record) error`, `Find(ctx context.Context, q Query) ([]Record, error)`, `Reset(ctx context.Context) error`, `Close() error`; `ports.Query{Kind string, Match map[string]string, Limit int}`; `sqlindex.Open(ctx context.Context, path string) (*sqlindex.Index, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/sqlindex/sqlite_test.go`:

```go
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
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapters/outbound/sqlindex/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the port**

Create `internal/core/ports/index.go`:

```go
package ports

import (
	"context"
	"time"
)

// Record is one indexed document. It is derived from Docs and never
// authoritative: anything here can be rebuilt from the record.
type Record struct {
	Path   string
	Rev    Revision
	Kind   string
	Fields map[string]string
	When   time.Time
}

// Query selects records. An empty Match matches every record of the Kind.
type Query struct {
	Kind  string
	Match map[string]string
	Limit int
}

// Index answers questions the record cannot answer cheaply.
type Index interface {
	Upsert(ctx context.Context, r Record) error
	Find(ctx context.Context, q Query) ([]Record, error)
	Reset(ctx context.Context) error
	Close() error
}
```

- [ ] **Step 4: Add the dependency**

```bash
go get modernc.org/sqlite@v1.59.0
go mod tidy
```

`modernc.org/sqlite` is pure Go and needs no cgo. Register it as the `sqlite` driver.

- [ ] **Step 5: Implement**

Create `internal/adapters/outbound/sqlindex/sqlite.go`. Schema:

```sql
CREATE TABLE IF NOT EXISTS records (
    path TEXT PRIMARY KEY,
    rev  TEXT NOT NULL,
    kind TEXT NOT NULL,
    when_utc INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS fields (
    path  TEXT NOT NULL REFERENCES records(path) ON DELETE CASCADE,
    name  TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (path, name)
);
CREATE INDEX IF NOT EXISTS records_kind_idx ON records (kind);
CREATE INDEX IF NOT EXISTS fields_lookup_idx ON fields (name, value);
```

`Upsert` writes both tables in one transaction, deleting the path's existing fields first so a removed field does not survive. `Find` builds one parameterised statement with an `INTERSECT` or a join per matched field. **Every value is a bound parameter.** The last test exists because string interpolation here would make a query value into SQL, and it will fail if you interpolate.

`Reset` deletes from both tables rather than dropping them, so the schema survives.

`path` is the primary key, which is what makes `Upsert` replace rather than duplicate.

- [ ] **Step 6: Run it and watch it pass**

Run: `go test ./internal/adapters/outbound/sqlindex/ -v`
Expected: PASS, all five tests.

- [ ] **Step 7: Commit**

```bash
git add internal/core/ports/index.go internal/adapters/outbound/sqlindex go.mod go.sum
git commit -m "Make the record queryable without reading it"
```

---

### Task 4: A DuckDB index of history

**Files:**
- Create: `internal/adapters/outbound/duckindex/duckdb.go`
- Test: `internal/adapters/outbound/duckindex/duckdb_test.go`

**Interfaces:**
- Consumes: `ports.Index`, `ports.Record`, `ports.Query`.
- Produces: `duckindex.Open(ctx context.Context, path string) (*duckindex.Index, error)`, implementing `ports.Index`.

This adapter answers a different question from Task 3's. SQLite holds one row per path — the current state. DuckDB holds **one row per revision**, so the same path appears many times and history can be scanned.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/outbound/duckindex/duckdb_test.go` with the same five behaviours as the SQLite suite, plus the one that distinguishes it:

```go
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
```

Write five more in the same file. Do not go and read Task 3 for them — here is what each must assert:

| Test | Asserts |
|---|---|
| `TestARecordIsFoundByField` | Upsert one record with `Fields{"state":"open"}`, then `Find(Query{Kind:"item", Match:{"state":"open"}})` returns exactly it, by path |
| `TestUpsertReplacesTheSameRevision` | Upsert the SAME path and SAME rev twice with different field values; `Find` returns one row carrying the second value. The primary key is `(path, rev)`, not `path` |
| `TestAQueryThatMatchesNothingIsNotAnError` | `Find` with a `Match` value nothing carries returns an empty slice and a nil error |
| `TestResetEmptiesTheIndex` | Upsert, `Reset`, then `Find` returns nothing |
| `TestAMatchValueContainingSQLIsTreatedAsData` | `Find` with `Match{"state": "' OR '1'='1"}` returns zero rows. If it returns rows, the value is being interpolated into SQL rather than bound |

The last one is not ceremony. Both index adapters build their WHERE clause from a caller-supplied map, and a query value reaching the statement as text rather than as a parameter is the difference between a filter and an injection.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/adapters/outbound/duckindex/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Add the dependency**

```bash
go get github.com/marcboeker/go-duckdb/v2@v2.4.3
go mod tidy
go build ./...
```

This is the first cgo dependency in the tree. `go build` may take noticeably longer on the first run while the bundled static library is linked. If the build fails for a toolchain reason — a missing C compiler, an unsupported platform — STOP and report the exact error. Do not attempt to install a toolchain.

- [ ] **Step 4: Implement**

Create `internal/adapters/outbound/duckindex/duckdb.go`. Schema:

```sql
CREATE TABLE IF NOT EXISTS revisions (
    path TEXT NOT NULL,
    rev  TEXT NOT NULL,
    kind TEXT NOT NULL,
    when_utc BIGINT NOT NULL,
    PRIMARY KEY (path, rev)
);
CREATE TABLE IF NOT EXISTS fields (
    path  TEXT NOT NULL,
    rev   TEXT NOT NULL,
    name  TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (path, rev, name)
);
```

Same binding discipline as Task 3: every query value is a bound parameter.

DuckDB has no `ON DELETE CASCADE`; delete a revision's fields explicitly before re-inserting them.

- [ ] **Step 5: Run it and watch it pass**

Run: `go test ./internal/adapters/outbound/duckindex/ -v`
Expected: PASS, all six tests.

- [ ] **Step 6: Confirm the binary is still one file**

```bash
go build -o /tmp/atlas ./cmd/atlas
ldd /tmp/atlas 2>&1 | head -5
```

Record what `ldd` reports. A dynamically linked binary against a system DuckDB would be a finding; the driver is expected to link its bundled library statically. Paste the output either way.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/outbound/duckindex go.mod go.sum
git commit -m "Keep every revision, so history can be scanned rather than replayed"
```

---

### Task 5: Rebuild from the record, and build on every platform

This is the story the whole increment exists for. An index that cannot be rebuilt is not derived — it is a second system of record that nobody is maintaining.

**Files:**
- Create: `internal/core/app/rebuild.go`, `.github/workflows/build.yml`, `docs/notes/2026-09-19-increment-1.md`
- Test: `internal/core/app/rebuild_test.go`
- Modify: `internal/config/config.go`, `internal/config/layers.go`, `cmd/atlas/main.go`

**Interfaces:**
- Consumes: `ports.Docs`, `ports.Index`, `ports.Record`.
- Produces: `app.Rebuild(ctx context.Context, docs ports.Docs, idx ports.Index, extract func(path string, body []byte) (ports.Record, bool)) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/app/rebuild_test.go`. It must use the REAL git store and the REAL SQLite index — a rebuild test against fakes proves nothing about the property.

```go
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
	for i := range before {
		if after[i].Path != before[i].Path || after[i].Fields["state"] != before[i].Fields["state"] {
			t.Errorf("record %d differs: before %+v, after %+v", i, before[i], after[i])
		}
	}
}
```

Write `extractItem` in the same file: a use-case-neutral extractor that indexes any path under `items/` with `Kind: "item"`, reading a `state: <value>` line from the body. It returns `false` for paths it does not recognise, which is what makes `other/three.md` absent from the results.

Add a second test asserting `Rebuild` calls `Reset` first, by pre-populating the index with a record whose path is not in the record store and asserting it is gone afterwards. A rebuild that appends rather than resets leaves stale rows forever.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/core/app/ -run TestDeletingTheIndex -v`
Expected: FAIL — `app.Rebuild` undefined.

- [ ] **Step 3: Implement**

Create `internal/core/app/rebuild.go`:

```go
// Rebuild discards the index and derives it again from the record. An index
// that cannot be rebuilt is not derived; it is a second system of record.
func Rebuild(ctx context.Context, docs ports.Docs, idx ports.Index, extract func(path string, body []byte) (ports.Record, bool)) error {
	if err := idx.Reset(ctx); err != nil {
		return fmt.Errorf("rebuild: reset: %w", err)
	}
	paths, err := docs.List(ctx, "")
	if err != nil {
		return fmt.Errorf("rebuild: list: %w", err)
	}
	for _, p := range paths {
		body, err := docs.Get(ctx, p)
		if err != nil {
			return fmt.Errorf("rebuild: read %s: %w", p, err)
		}
		rec, ok := extract(p, body)
		if !ok {
			continue
		}
		if err := idx.Upsert(ctx, rec); err != nil {
			return fmt.Errorf("rebuild: index %s: %w", p, err)
		}
	}
	return nil
}
```

**`internal/core/app` must not import `gitdocs` or `sqlindex`.** The test file is `package app_test`, which may import adapters; the implementation may not. `internal/arch/arch_test.go` will catch it if this slips.

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/core/app/ -v`
Expected: PASS.

- [ ] **Step 5: Wire config and the composition root**

Add to `internal/config`: `Store.Root` (default `~/.atlas/workspace`), `Store.IndexPath` (default `~/.atlas/index.db`), `Store.HistoryPath` (default `~/.atlas/history.duckdb`), each with a flag and an environment variable following the existing pattern, and validation rejecting an empty value.

Expand `~` at load time rather than storing a literal home path, and fail loudly if the home directory cannot be determined.

In `cmd/atlas/main.go`, open the store and the indices and close them on the way out. Remember the constraint from the previous increment: `main()` holds no defer, so the cleanup belongs in `run()`.

- [ ] **Step 6: Add the build matrix**

Create `.github/workflows/build.yml` building on `ubuntu-latest`, `macos-latest` and `windows-latest` with Go 1.27, running `go build ./...` and `go test ./... -race`.

Cross-compiling from one machine will not work now that DuckDB requires cgo, which is the whole reason this matrix exists. State that in one clause in the workflow file.

- [ ] **Step 7: Run everything**

```bash
go build ./... && go vet ./... && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Both packs must still run unaided. This increment adds storage; it must not have changed how a pack executes.

- [ ] **Step 8: Write the increment note**

Create `docs/notes/2026-09-19-increment-1.md` with the four sections: what the pattern was, what surprised you, what you would do differently, what you still do not understand.

Record honestly:

- Whether `ldd` showed a static or dynamic binary, and what that means for shipping one file.
- What go-git did differently from the real git, if anything bit you.
- Whether the rebuild test would actually have caught a stale-row bug, or whether it only proves the happy path.
- Whether `Record.Fields` being `map[string]string` was the right shape, or whether the first real consumer will want something richer.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "Prove the indices are derived by throwing one away and rebuilding it"
```

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| Nerve as a `Docs` implementation | The port names it as the second implementation; nothing needs it yet |
| Pushing the repository to a remote | The user can add one with git; the app does not need to know |
| Encryption at rest | The repository is on the user's own machine under their own account |
| A migration story for the schemas | Both indices are derived; the migration is a rebuild |
| Indexing anything use-case shaped | There is nothing to index yet. `extractItem` is a neutral placeholder and must stay one |

# The record

How the storage layer works today. Companion to the interactive map at
`docs/diagrams/atlas-record.html`.

For what was decided and why, see `docs/specs/2026-09-17-job-hunt-harness-design.md`.
For what building it taught, see `docs/notes/2026-09-19-increment-1.md`.

## The shape

Git is the system of record. Two indices are derived from it and are never
authoritative: deleting either and rebuilding from git alone loses nothing, and a
test asserts that rather than a comment claiming it.

| Port | Adapter | Holds |
|---|---|---|
| `Docs` | `gitdocs`, over go-git v5 | Every revision of every document |
| `Index` | `sqlindex`, over `modernc.org/sqlite` | One row per path — current state |
| `Index` | `duckindex`, over `marcboeker/go-duckdb` | One row per `(path, rev)` — history |

`Index` earns its place as a port because both implementations are real and
shipped. They differ in **cardinality model**, not merely in engine.

## Reads resolve at HEAD

`Get` and `List` read git objects, not the filesystem. The working tree is a
materialisation of the record, not the record.

This is what makes "rebuild from git alone" literally true. If reads came off the
working tree, a document removed from disk would vanish from a rebuild while git
still held every revision of it — and a revision-keyed index would lose exactly
the history it exists to keep.

It also means a path staged but not committed is invisible to every reader, which
is correct: nothing in the codebase reads git's index.

## A git path is slash-separated, always

`safeRelPath` validates with the standard library's `path` package, never
`path/filepath`, and returns slash-canonical form. `filepath.FromSlash` is applied
at the single point that touches disk.

This is not cosmetic. `filepath.Clean("notes/one.md")` returns `notes\one.md` on
Windows, and git tree entries are slash-separated on every platform, so an
OS-separated path silently misses every tree lookup.

A path is refused rather than cleaned when it is empty, absolute, contains a
backslash, or escapes the root once resolved. A cleaned path and a refused path
are indistinguishable to a caller that then writes to the wrong place.

## A byte-identical write is a no-op

`Put` compares the incoming body against the blob HEAD holds for that path. If
they match it returns the existing revision without committing.

The alternative — recording a re-assertion as its own revision — is not
implementable. Git has no representation for it: a commit whose tree is unchanged
records no path at all, so `History`, which enumerates by diff, can never list it.
A revision `Put` returns that `History` will not list is worse than a no-op.

`Put` writes the worktree, stages, then commits. On a staging or commit failure it
restores that one path to what it held before the call.

## Two walks, because two shapes

| Function | Enumerates | For |
|---|---|---|
| `Rebuild` | `List` then `Get` — the current tree | A path-keyed index |
| `RebuildHistory` | `List`, then `History` and `GetAt` per path | A revision-keyed index |

One function cannot serve both. Upserting every revision into a path-keyed table
is last-write-wins, and `History` returns newest-first, so the surviving row would
be the *oldest* revision — wrong, silently, with a nil error.

Passing the wrong index to either is a **compile error**. Each adapter carries a
nullary marker method, and `internal/core/app` declares consumer-side interfaces
that embed `ports.Index` plus the marker it requires. The markers are not on
`ports.Index`; they are the consumer's discrimination, declared at the consumer.

`extract` interprets a body into `Kind` and `Fields` and never owns identity. Both
walks stamp `Rev` and `When` from the revision's own metadata.

## Index schemas

Both are derived, so both are disposable. Neither holds anything a rebuild could
not reconstruct — no autoincrement id, no insertion timestamp, no sequence number.

`sqlindex` keys `records` by `path`. `duckindex` keys `revisions` by
`(path, rev)`, and scopes a field delete to `(path, rev)` so re-indexing one
revision cannot destroy another's fields.

Every query value is a bound parameter, including the field *name*, which is a map
key and the easiest one to interpolate by accident.

`When` is stored as `UnixNano` and compared with `.Equal()`, never `==` — `==`
also compares the monotonic reading and `*Location`, which no database round trip
preserves.

## Known gaps

- `Rebuild` and `RebuildHistory` have **no production caller**. The compile-time
  guard protects tests until one exists.
- `ports.Query` has no revision vocabulary — no "at revision X", no "newest per
  path" — so `duckindex` is only half-addressable through its own port.
- `Rebuild` indexes `history[0]` unchecked. Unreachable through `gitdocs`, but
  `Rebuild` takes `ports.Docs`; a second implementation whose `List` and `History`
  disagree would panic rather than error.
- `time.Time{}` does not survive a `UnixNano` round trip as `IsZero()` — year-one
  nanoseconds overflow `int64` and return as 1754. An `IsZero()` check on a
  genuinely unset `When` would silently fail.
- DuckDB is the only cgo dependency, so binaries are built per platform rather
  than cross-compiled.

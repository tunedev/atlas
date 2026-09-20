// Package gitdocs implements ports.Docs over a git worktree. Every Put is a
// commit; nothing is overwritten in place.
package gitdocs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tunedev/atlas/internal/core/ports"
)

// commitAuthorName and commitAuthorEmail identify the committer for every
// write. Both are fixed for now; config wiring for them lands separately.
const (
	commitAuthorName  = "atlas"
	commitAuthorEmail = "atlas@localhost"
)

// Store implements ports.Docs over a git repository rooted at a directory on
// disk.
type Store struct {
	repo *git.Repository
	root string
}

// Open opens the git repository at root, initialising one if none exists yet.
func Open(ctx context.Context, root string) (*Store, error) {
	repo, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInit(root, false)
	}
	if err != nil {
		return nil, fmt.Errorf("gitdocs: open repository at %s: %w", root, err)
	}
	return &Store{repo: repo, root: root}, nil
}

// Put writes body to path, committing the change, and returns the resulting
// revision. A body byte-identical to what HEAD already holds at path is a
// no-op: Put returns that path's existing revision without touching the
// working tree or creating a commit. This is not a policy choice but the
// only representable behaviour — git has no way to record "this path was
// re-asserted unchanged"; an empty commit records no path at all, so there
// is no revision such a write could produce that History could ever
// enumerate. If staging or committing fails after the working tree write,
// path is restored to whatever it held before this call. Get and List read
// from HEAD, not the working tree, so the working tree is a materialisation
// of the record, not the record itself.
func (s *Store) Put(ctx context.Context, path string, body []byte, message string) (ports.Revision, error) {
	rel, err := safeRelPath(path)
	if err != nil {
		return "", err
	}

	if rev, ok, err := s.unchangedRevision(ctx, path, rel, body); err != nil {
		return "", err
	} else if ok {
		return rev, nil
	}

	full := filepath.Join(s.root, filepath.FromSlash(rel))
	prior, priorErr := os.ReadFile(full)
	hadPrior := priorErr == nil

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("gitdocs: create directories for %s: %w", path, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		return "", fmt.Errorf("gitdocs: write %s: %w", path, err)
	}

	wt, err := s.repo.Worktree()
	if err != nil {
		return "", rollback(full, hadPrior, prior, fmt.Errorf("gitdocs: worktree: %w", err))
	}
	if _, err := wt.Add(rel); err != nil {
		return "", rollback(full, hadPrior, prior, fmt.Errorf("gitdocs: stage %s: %w", path, err))
	}

	author := &object.Signature{
		Name:  commitAuthorName,
		Email: commitAuthorEmail,
		When:  time.Now(),
	}
	hash, err := wt.Commit(message, &git.CommitOptions{Author: author})
	if err != nil {
		return "", rollback(full, hadPrior, prior, fmt.Errorf("gitdocs: commit %s: %w", path, err))
	}
	return ports.Revision(hash.String()), nil
}

// unchangedRevision reports whether body is byte-identical to what HEAD
// already holds at path. When it is, it also returns the revision that
// produced it, taken from History rather than HEAD itself, since HEAD may by
// now point at a commit that changed a different path.
func (s *Store) unchangedRevision(ctx context.Context, path, rel string, body []byte) (ports.Revision, bool, error) {
	tree, err := s.headTree()
	if err != nil {
		return "", false, fmt.Errorf("gitdocs: check %s: %w", path, err)
	}
	if tree == nil {
		return "", false, nil
	}
	file, err := tree.File(rel)
	if errors.Is(err, object.ErrFileNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("gitdocs: check %s: %w", path, err)
	}
	contents, err := file.Contents()
	if err != nil {
		return "", false, fmt.Errorf("gitdocs: check %s: %w", path, err)
	}
	if !bytes.Equal([]byte(contents), body) {
		return "", false, nil
	}

	history, err := s.History(ctx, path)
	if err != nil {
		return "", false, fmt.Errorf("gitdocs: check %s: %w", path, err)
	}
	if len(history) == 0 {
		return "", false, nil
	}
	return history[0].Rev, true, nil
}

// rollback restores full to the state it held before a Put that failed after
// already writing it: removed if it did not exist before, rewritten with
// prior if it did. It returns cause, joined with any error the restore
// itself hits.
func rollback(full string, hadPrior bool, prior []byte, cause error) error {
	var rbErr error
	if hadPrior {
		rbErr = os.WriteFile(full, prior, 0o644)
	} else {
		rbErr = os.Remove(full)
	}
	if rbErr != nil {
		return errors.Join(cause, fmt.Errorf("gitdocs: rollback %s: %w", full, rbErr))
	}
	return cause
}

// Get reads the body of path as committed at HEAD. It is GetAt applied to
// the current revision.
func (s *Store) Get(ctx context.Context, path string) ([]byte, error) {
	ref, err := s.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("gitdocs: read %s: %w", path, err)
	}
	return s.GetAt(ctx, path, ports.Revision(ref.Hash().String()))
}

// List returns the slash-separated paths of every file committed at HEAD
// under prefix. prefix is a directory boundary, not a literal string prefix:
// "a" and "a/" both match "a/one.md" but neither matches "ab/two.md". An
// empty prefix matches every path. A repository with no commits yet has no
// HEAD tree to walk and returns no paths.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	boundary := strings.TrimSuffix(prefix, "/")
	tree, err := s.headTree()
	if err != nil {
		return nil, fmt.Errorf("gitdocs: list under %s: %w", prefix, err)
	}
	if tree == nil {
		return nil, nil
	}

	var paths []string
	iter := tree.Files()
	defer iter.Close()
	err = iter.ForEach(func(f *object.File) error {
		if boundary == "" || strings.HasPrefix(f.Name, boundary+"/") {
			paths = append(paths, f.Name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitdocs: list under %s: %w", prefix, err)
	}
	return paths, nil
}

// headTree returns the tree HEAD points at, or nil if the repository has no
// commits yet.
func (s *Store) headTree() (*object.Tree, error) {
	ref, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD: %w", err)
	}
	commit, err := s.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("load HEAD commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("load HEAD tree: %w", err)
	}
	return tree, nil
}

// safeRelPath validates p as a slash-separated git path and returns its
// cleaned, slash-separated form. Git tree paths are always slash-separated
// regardless of platform, so validation uses "path", not "path/filepath":
// filepath's rules follow the host OS and would accept or reject different
// inputs on Windows than on Linux or macOS. p is rejected if it is empty,
// starts with "/", contains a backslash, or escapes the root once cleaned.
// A backslash is rejected outright rather than treated as a separator: on
// this slash-canonical interface it is a literal character in a filename,
// and treating it as anything else invites exactly the ambiguity this
// function exists to remove. It refuses rather than normalises: a cleaned
// path and a refused path are indistinguishable to a caller that then
// writes to the wrong place.
func safeRelPath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return "", fmt.Errorf("gitdocs: path %q must be relative and non-empty", p)
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("gitdocs: path %q escapes the root", p)
	}
	return clean, nil
}

// Package gitdocs implements ports.Docs over a git worktree. Every Put is a
// commit; nothing is overwritten in place.
package gitdocs

import (
	"context"
	"errors"
	"fmt"
	"os"
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
// revision. A body identical to what is already committed at path still
// produces a new revision: git's own empty-commit rejection is disabled
// deliberately, so writing the same content again remains a recorded event
// with its own message and timestamp rather than a silent no-op. If staging
// or committing fails, path is restored to whatever it held before this
// call: the working tree never diverges from the last successful commit.
// Get and List read from HEAD, not the working tree, so this restore is
// about leaving the working tree consistent, not about read safety.
func (s *Store) Put(ctx context.Context, path string, body []byte, message string) (ports.Revision, error) {
	rel, err := safeRelPath(path)
	if err != nil {
		return "", err
	}

	full := filepath.Join(s.root, rel)
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
	hash, err := wt.Commit(message, &git.CommitOptions{Author: author, AllowEmptyCommits: true})
	if err != nil {
		return "", rollback(full, hadPrior, prior, fmt.Errorf("gitdocs: commit %s: %w", path, err))
	}
	return ports.Revision(hash.String()), nil
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
		return nil, fmt.Errorf("head: %w", err)
	}
	commit, err := s.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("head commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("head tree: %w", err)
	}
	return tree, nil
}

// safeRelPath cleans p and rejects it if it is empty, absolute, or escapes
// the root once cleaned. It refuses rather than normalises: a cleaned path
// and a refused path are indistinguishable to a caller that then writes to
// the wrong place.
func safeRelPath(p string) (string, error) {
	if p == "" || filepath.IsAbs(p) {
		return "", fmt.Errorf("gitdocs: path %q must be relative and non-empty", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("gitdocs: path %q escapes the root", p)
	}
	return clean, nil
}

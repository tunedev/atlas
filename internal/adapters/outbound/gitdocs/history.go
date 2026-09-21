package gitdocs

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tunedev/atlas/internal/core/ports"
)

// History returns the revisions of path, newest first, as recorded by git.
func (s *Store) History(ctx context.Context, path string) ([]ports.DocMeta, error) {
	rel, err := safeRelPath(path)
	if err != nil {
		return nil, err
	}

	fileName := rel
	commits, err := s.repo.Log(&git.LogOptions{FileName: &fileName})
	if err != nil {
		return nil, fmt.Errorf("gitdocs: history of %s: %w", path, err)
	}
	defer commits.Close()

	var history []ports.DocMeta
	err = commits.ForEach(func(c *object.Commit) error {
		history = append(history, ports.DocMeta{
			Path:    path,
			Rev:     ports.Revision(c.Hash.String()),
			When:    c.Author.When,
			Message: c.Message,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitdocs: history of %s: %w", path, err)
	}
	return history, nil
}

// GetAt reads the body path held at rev.
func (s *Store) GetAt(ctx context.Context, path string, rev ports.Revision) ([]byte, error) {
	rel, err := safeRelPath(path)
	if err != nil {
		return nil, err
	}

	commit, err := s.repo.CommitObject(plumbing.NewHash(string(rev)))
	if err != nil {
		return nil, fmt.Errorf("gitdocs: resolve revision %s for %s: %w", rev, path, err)
	}
	file, err := commit.File(rel)
	if err != nil {
		return nil, fmt.Errorf("gitdocs: read %s at revision %s: %w", path, rev, err)
	}
	contents, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("gitdocs: read %s at revision %s: %w", path, rev, err)
	}
	return []byte(contents), nil
}

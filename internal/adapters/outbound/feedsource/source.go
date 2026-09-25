// Package feedsource implements ports.Source over a git remote. It keeps a
// bare cache holding only the tip of one pinned ref: each Pull fetches that
// ref at depth one and reads every JSON document in the tip's tree. It only
// ever fetches from the remote.
package feedsource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tunedev/atlas/internal/core/ports"
)

// pinned is where the cache keeps the fetched tip of the configured ref.
const pinned = plumbing.ReferenceName("refs/atlas/pinned")

// Config names the remote, the full ref to follow (refs/heads/... or
// refs/tags/...), the cache directory, and the bound on one Pull.
type Config struct {
	RemoteURL   string
	Ref         string
	CachePath   string
	PullTimeout time.Duration
}

type Source struct {
	cfg Config
}

// New does no I/O; the cache is created by the first Pull.
func New(cfg Config) *Source { return &Source{cfg: cfg} }

// Pull fetches the pinned ref's tip and returns every JSON document in its
// tree, sorted by id. An item's ID is its path minus ".json"; its When is
// the tip commit's time.
func (s *Source) Pull(ctx context.Context) ([]ports.Item, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PullTimeout)
	defer cancel()

	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	err = repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: "origin",
		Depth:      1,
		Force:      true,
		Tags:       git.NoTags,
		RefSpecs:   []config.RefSpec{config.RefSpec("+" + s.cfg.Ref + ":" + string(pinned))},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil, fmt.Errorf("feedsource: fetch %s from %s: %w", s.cfg.Ref, s.cfg.RemoteURL, err)
	}
	commit, err := pinnedCommit(repo)
	if err != nil {
		return nil, err
	}
	return items(commit)
}

// LastRefreshed is the time of the tip commit the last Pull fetched. It
// fetches nothing, and fails if nothing has been pulled yet.
func (s *Source) LastRefreshed(ctx context.Context) (time.Time, error) {
	repo, err := git.PlainOpen(s.cfg.CachePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("feedsource: nothing pulled into %s yet: %w", s.cfg.CachePath, err)
	}
	commit, err := pinnedCommit(repo)
	if err != nil {
		return time.Time{}, err
	}
	return commit.Committer.When, nil
}

// open opens the cache, creating a bare repository tracking RemoteURL on
// first use. A cache already tracking a different remote fails rather than
// serving one feed's documents under another's name.
func (s *Source) open() (*git.Repository, error) {
	repo, err := git.PlainOpen(s.cfg.CachePath)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInit(s.cfg.CachePath, true)
		if err == nil {
			_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{s.cfg.RemoteURL}})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("feedsource: open cache %s: %w", s.cfg.CachePath, err)
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return nil, fmt.Errorf("feedsource: cache %s: %w", s.cfg.CachePath, err)
	}
	if urls := remote.Config().URLs; len(urls) != 1 || urls[0] != s.cfg.RemoteURL {
		return nil, fmt.Errorf("feedsource: cache %s tracks %v, config names %s; point CachePath elsewhere or delete the cache",
			s.cfg.CachePath, urls, s.cfg.RemoteURL)
	}
	return repo, nil
}

// pinnedCommit resolves the pinned ref to its commit, peeling an annotated
// tag.
func pinnedCommit(repo *git.Repository) (*object.Commit, error) {
	ref, err := repo.Reference(pinned, true)
	if err != nil {
		return nil, fmt.Errorf("feedsource: resolve %s: %w", pinned, err)
	}
	if tag, err := repo.TagObject(ref.Hash()); err == nil {
		commit, err := tag.Commit()
		if err != nil {
			return nil, fmt.Errorf("feedsource: peel tag %s: %w", tag.Name, err)
		}
		return commit, nil
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("feedsource: load %s: %w", ref.Hash(), err)
	}
	return commit, nil
}

func items(commit *object.Commit) ([]ports.Item, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("feedsource: load tree of %s: %w", commit.Hash, err)
	}
	var out []ports.Item
	err = tree.Files().ForEach(func(f *object.File) error {
		if !strings.HasSuffix(f.Name, ".json") {
			return nil
		}
		body, err := f.Contents()
		if err != nil {
			return fmt.Errorf("read %s: %w", f.Name, err)
		}
		out = append(out, ports.Item{ID: strings.TrimSuffix(f.Name, ".json"), Body: []byte(body), When: commit.Committer.When})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("feedsource: %w", err)
	}
	slices.SortFunc(out, func(a, b ports.Item) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

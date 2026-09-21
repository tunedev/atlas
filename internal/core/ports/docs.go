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

// Docs is the system of record. A write that changes a path's body produces
// a new revision; a write that repeats the current body exactly is a no-op
// and returns that path's current revision. Nothing already committed is
// ever overwritten in place.
type Docs interface {
	Put(ctx context.Context, path string, body []byte, message string) (Revision, error)
	Get(ctx context.Context, path string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
	History(ctx context.Context, path string) ([]DocMeta, error)
	GetAt(ctx context.Context, path string, rev Revision) ([]byte, error)
}

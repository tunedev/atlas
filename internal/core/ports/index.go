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

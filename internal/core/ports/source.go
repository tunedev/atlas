package ports

import (
	"context"
	"time"
)

// Item is one document a Source yields, addressed by ID. Body is opaque:
// the core does not parse it, the same way Docs never parses what it stores.
// A pack narrows Body by path, the way it already narrows any tool's result.
type Item struct {
	ID   string
	Body []byte
	When time.Time
}

// Source answers what is there now, and how fresh that answer is. Pull
// returns every item present; nothing about "new since last time" is the
// port's concern, so a git remote, a local crawler, and a rendered fetch all
// answer the same two questions the same way.
//
// A Source made of several parts, such as a crawler over many pages, may
// return the items it reached together with an error naming the parts that
// failed; it returns an error alone only when nothing was reached.
type Source interface {
	Pull(ctx context.Context) ([]Item, error)
	LastRefreshed(ctx context.Context) (time.Time, error)
}

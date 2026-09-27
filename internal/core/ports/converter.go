package ports

import (
	"context"
	"io"
)

// Format names a document format, such as "typst" or "pdf".
type Format string

// Pair is the source and target format one Converter serves.
type Pair struct {
	From Format
	To   Format
}

// Converter turns a document in one format into another. It streams src to
// dst and writes nothing to dst when it fails.
type Converter interface {
	Pair() Pair
	Convert(ctx context.Context, dst io.Writer, src io.Reader) error
}

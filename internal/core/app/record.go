package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// recordTimeFormat is RFC 3339 with colons replaced by dashes, since a colon
// is not portable in a path component, at millisecond precision so two
// records of one subject less than a second apart land at distinct paths.
const recordTimeFormat = "2006-01-02T15-04-05.000Z"

// recordDocTimeFormat is RFC 3339 at the same precision as recordTimeFormat,
// so a document's own time and its path order records identically.
const recordDocTimeFormat = "2006-01-02T15:04:05.000Z"

// Document is one body to commit to the record and the flat index row that
// finds it.
type Document struct {
	Path    string
	Body    []byte
	Message string
	Kind    string
	Fields  map[string]string
	When    time.Time
}

// RecordDocument commits d.Body at d.Path, then indexes it under d.Kind with
// d.Fields. Git holds the document; the row only helps find it. A failed
// upsert after a successful put still returns the revision, since the
// document is recorded and the index can be rebuilt from it.
func RecordDocument(ctx context.Context, docs ports.Docs, index ports.Index, d Document) (ports.Revision, error) {
	if d.Path == "" {
		return "", errors.New("record: path is empty")
	}
	if path.Clean(d.Path) != d.Path || d.Path == ".." || strings.HasPrefix(d.Path, "../") || path.IsAbs(d.Path) {
		return "", fmt.Errorf("record: path %q is not a clean relative path", d.Path)
	}
	if d.Kind == "" {
		return "", fmt.Errorf("record: %s: kind is empty", d.Path)
	}

	rev, err := docs.Put(ctx, d.Path, d.Body, d.Message)
	if err != nil {
		return "", fmt.Errorf("record: put %s: %w", d.Path, err)
	}

	fields := d.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	row := ports.Record{Path: d.Path, Rev: rev, Kind: d.Kind, Fields: fields, When: d.When}
	if err := index.Upsert(ctx, row); err != nil {
		return rev, fmt.Errorf("record: upsert %s: %w", d.Path, err)
	}
	return rev, nil
}

// checkSubjectID rejects a subject id that could carry a document outside
// its own directory: one containing a path separator or a ".." segment.
func checkSubjectID(id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		return fmt.Errorf("subject id %q is not a plain name", id)
	}
	return nil
}

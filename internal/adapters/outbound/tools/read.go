package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// IndexFind returns the index rows of one kind whose fields equal every
// value in match. It reads the derived index, never the record.
type IndexFind struct{ index ports.Index }

func NewIndexFind(index ports.Index) *IndexFind { return &IndexFind{index: index} }

func (f *IndexFind) Name() string { return "index.find" }

func (f *IndexFind) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if with["kind"] == "" {
		return nil, fmt.Errorf("index.find: no kind")
	}
	match, err := parseFields(with["match"])
	if err != nil {
		return nil, fmt.Errorf("index.find: %w", err)
	}
	limit := 0
	if raw := with["limit"]; raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit <= 0 {
			return nil, fmt.Errorf("index.find: limit must be a positive integer, got %q", raw)
		}
	}
	recs, err := f.index.Find(ctx, ports.Query{Kind: with["kind"], Match: match, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("index.find: %w", err)
	}
	rows := make([]map[string]any, len(recs))
	for i, r := range recs {
		rows[i] = map[string]any{
			"path": r.Path, "rev": string(r.Rev), "kind": r.Kind,
			"fields": r.Fields, "when": r.When.UTC().Format(time.RFC3339),
		}
	}
	return map[string]any{"rows": rows}, nil
}

// DocsGet reads one document from the record, at its latest revision or at
// rev. A body that is valid JSON is also returned parsed, as doc.
type DocsGet struct {
	docs     ports.Docs
	maxBytes int64
}

func NewDocsGet(docs ports.Docs, maxBytes int64) *DocsGet {
	return &DocsGet{docs: docs, maxBytes: maxBytes}
}

func (g *DocsGet) Name() string { return "docs.get" }

func (g *DocsGet) Invoke(ctx context.Context, with map[string]string) (any, error) {
	path := with["path"]
	if path == "" {
		return nil, fmt.Errorf("docs.get: no path")
	}
	var body []byte
	var err error
	if rev := with["rev"]; rev != "" {
		body, err = g.docs.GetAt(ctx, path, ports.Revision(rev))
	} else {
		body, err = g.docs.Get(ctx, path)
	}
	if err != nil {
		return nil, fmt.Errorf("docs.get: %s: %w", path, err)
	}
	if int64(len(body)) > g.maxBytes {
		return nil, fmt.Errorf("docs.get: %s is %d bytes, over the %d-byte bound", path, len(body), g.maxBytes)
	}
	out := map[string]any{"path": path, "text": string(body)}
	var doc any
	if json.Unmarshal(body, &doc) == nil {
		out["doc"] = doc
	}
	return out, nil
}

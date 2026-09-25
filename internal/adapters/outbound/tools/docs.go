package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// DocsPut commits a body to the record and indexes it, so an edit a person
// made on disk becomes a revision and stays findable. A byte-identical body
// is a no-op revision, per ports.Docs.
type DocsPut struct {
	docs  ports.Docs
	index ports.Index
}

func NewDocsPut(docs ports.Docs, index ports.Index) *DocsPut {
	return &DocsPut{docs: docs, index: index}
}

func (d *DocsPut) Name() string { return "docs.put" }

func (d *DocsPut) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if err := checkExpect(with["expect"], with["body"]); err != nil {
		return nil, fmt.Errorf("docs.put: %s: %w", with["path"], err)
	}
	fields, err := parseFields(with["fields"])
	if err != nil {
		return nil, fmt.Errorf("docs.put: %w", err)
	}
	message := with["message"]
	if message == "" {
		message = "Update " + with["path"]
	}

	rev, err := app.RecordDocument(ctx, d.docs, d.index, app.Document{
		Path:    with["path"],
		Body:    []byte(with["body"]),
		Message: message,
		Kind:    with["kind"],
		Fields:  fields,
		When:    time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("docs.put: %w", err)
	}
	return map[string]any{"path": with["path"], "rev": string(rev)}, nil
}

// checkExpect rejects a body that is not what expect names. Only json is
// known; an empty expect checks nothing.
func checkExpect(expect, body string) error {
	switch expect {
	case "":
		return nil
	case "json":
		if !json.Valid([]byte(body)) {
			return fmt.Errorf("body is not valid json")
		}
		return nil
	default:
		return fmt.Errorf("unknown expect %q", expect)
	}
}

// parseFields decodes a flat YAML map of index fields. Empty is no fields.
func parseFields(raw string) (map[string]string, error) {
	fields := map[string]string{}
	if err := yaml.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("parse fields: %w", err)
	}
	return fields, nil
}

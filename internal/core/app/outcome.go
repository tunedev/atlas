package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Outcome is the real-world result of a judged subject. State is a free
// string a pack defines the meaning of; When is when the state became true,
// not when it was recorded. A judgement whose outcome is still null has no
// Outcome yet.
type Outcome struct {
	State string
	When  time.Time
	Note  string
}

// outcomeDoc is an Outcome as it appears in a judgement document.
type outcomeDoc struct {
	State string `json:"state"`
	When  string `json:"when"`
	Note  string `json:"note"`
}

// AttachOutcome writes o into the outcome slot of the judgement document at
// judgementPath as a new revision of that document, and sets its index
// row's outcome field to o.State. Every other key of the document and every
// other field of the row stays as it was. Attaching again replaces the
// outcome, and history keeps the earlier one; attaching the same outcome
// again changes nothing.
func AttachOutcome(ctx context.Context, docs ports.Docs, index ports.Index, judgementPath string, o Outcome) (ports.Revision, error) {
	if path.Clean(judgementPath) != judgementPath || !strings.HasPrefix(judgementPath, "judgements/") || !strings.HasSuffix(judgementPath, ".json") {
		return "", fmt.Errorf("outcome: %q is not a judgement document", judgementPath)
	}
	if strings.TrimSpace(o.State) == "" {
		return "", errors.New("outcome: state is empty")
	}
	if o.When.IsZero() {
		return "", errors.New("outcome: when is not set")
	}

	body, err := docs.Get(ctx, judgementPath)
	if err != nil {
		return "", fmt.Errorf("outcome: read %s: %w", judgementPath, err)
	}
	var doc judgementDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("outcome: decode %s: %w", judgementPath, err)
	}
	if doc.SubjectID == "" {
		return "", fmt.Errorf("outcome: %s has no subject id", judgementPath)
	}

	value, err := json.Marshal(outcomeDoc{State: o.State, When: o.When.UTC().Format(recordDocTimeFormat), Note: o.Note})
	if err != nil {
		return "", fmt.Errorf("outcome: encode: %w", err)
	}
	updated, err := spliceKey(body, "outcome", value)
	if err != nil {
		return "", fmt.Errorf("outcome: %s: %w", judgementPath, err)
	}
	fields, when, err := currentRow(ctx, index, judgementPath, doc)
	if err != nil {
		return "", err
	}
	fields["outcome"] = o.State

	rev, err := RecordDocument(ctx, docs, index, Document{
		Path:    judgementPath,
		Body:    updated,
		Message: "Attach outcome for " + doc.SubjectID,
		Kind:    "judgement",
		Fields:  fields,
		When:    when,
	})
	if err != nil {
		return rev, fmt.Errorf("outcome: %w", err)
	}
	return rev, nil
}

// currentRow returns a copy of the fields and the time of path's index row.
// When the index has no row for path, it returns the fields a judgement is
// recorded with, read from its document.
func currentRow(ctx context.Context, index ports.Index, path string, doc judgementDoc) (map[string]string, time.Time, error) {
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: map[string]string{"subject_id": doc.SubjectID}})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("outcome: find %s: %w", path, err)
	}
	for _, r := range rows {
		if r.Path == path {
			fields := maps.Clone(r.Fields)
			if fields == nil {
				fields = map[string]string{}
			}
			return fields, r.When, nil
		}
	}
	when, err := time.Parse(recordDocTimeFormat, doc.When)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("outcome: %s: when %q: %w", path, doc.When, err)
	}
	ids := make([]string, len(doc.Questions))
	for i, q := range doc.Questions {
		ids[i] = q.ID
	}
	return map[string]string{
		"subject_id": doc.SubjectID,
		"model":      doc.Model,
		"provider":   doc.Provider,
		"questions":  strings.Join(ids, ","),
	}, when, nil
}

// spliceKey returns the JSON object body with key's value replaced by value.
// Every other key keeps its value and its place, and the object is indented
// two spaces, the way judgement documents are written. It fails when body is
// not an object or has no such key.
func spliceKey(body []byte, key string, value json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out bytes.Buffer
	out.WriteString("{")
	found := false
	for i := 0; dec.More(); i++ {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if k == key {
			raw, found = value, true
		}
		name, _ := json.Marshal(k)
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString("\n  ")
		out.Write(name)
		out.WriteString(": ")
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, err
		}
		if err := json.Indent(&out, compact.Bytes(), "  ", "  "); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("no %q key", key)
	}
	out.WriteString("\n}")
	return out.Bytes(), nil
}

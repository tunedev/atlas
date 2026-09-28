package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Stage is one point a subject's timeline reached. Stage is a free string a
// pack defines the meaning of; When is when it was reached, and now when
// zero.
type Stage struct {
	SubjectID  string
	Stage      string
	Note       string
	DeclaredBy string
	When       time.Time
}

// stageDoc is a subject's stage document: its current stage and the last
// time each stage it reached was reached, in RFC 3339.
type stageDoc struct {
	SubjectID  string            `json:"subject_id"`
	Stage      string            `json:"stage"`
	Reached    map[string]string `json:"reached"`
	Note       string            `json:"note"`
	DeclaredBy string            `json:"declared_by"`
}

// DeclareStage records s as a new revision of its subject's stage document
// and indexes it. It returns the path and whether anything changed. A time
// in the future, or before the current stage's time, is refused; declaring
// the current stage again changes nothing.
func DeclareStage(ctx context.Context, docs ports.Docs, index ports.Index, s Stage, now time.Time) (string, bool, error) {
	if err := checkStage(s); err != nil {
		return "", false, err
	}
	when := s.When
	if when.IsZero() {
		when = now
	}
	when = when.UTC().Truncate(time.Second)
	if when.After(now) {
		return "", false, fmt.Errorf("stage: %s at %s is in the future", s.Stage, when.Format(time.RFC3339))
	}

	path := "applications/" + s.SubjectID + "/stage.json"
	doc, err := readStage(ctx, docs, path)
	if err != nil {
		return "", false, err
	}
	if doc.Stage == s.Stage {
		return path, false, nil
	}
	if err := checkNotBeforeCurrent(doc, s.Stage, when); err != nil {
		return "", false, err
	}

	doc.SubjectID, doc.Stage, doc.Note, doc.DeclaredBy = s.SubjectID, s.Stage, s.Note, s.DeclaredBy
	doc.Reached[s.Stage] = when.Format(time.RFC3339)
	if err := writeStage(ctx, docs, index, path, doc, when); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// stageName is what a stage may be called, since it also names the
// <stage>_at field.
var stageName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func checkStage(s Stage) error {
	if s.SubjectID == "" {
		return errors.New("stage: subject id is empty")
	}
	if err := CheckSubjectID(s.SubjectID); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if !stageName.MatchString(s.Stage) {
		return fmt.Errorf("stage: name %q is not lowercase letters, digits and underscores", s.Stage)
	}
	return nil
}

// readStage returns the stage document at path, or an empty one when the
// subject has none.
func readStage(ctx context.Context, docs ports.Docs, path string) (stageDoc, error) {
	doc := stageDoc{Reached: map[string]string{}}
	paths, err := docs.List(ctx, strings.TrimSuffix(path, "/stage.json"))
	if err != nil {
		return doc, fmt.Errorf("stage: list %s: %w", path, err)
	}
	if !slices.Contains(paths, path) {
		return doc, nil
	}
	body, err := docs.Get(ctx, path)
	if err != nil {
		return doc, fmt.Errorf("stage: read %s: %w", path, err)
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("stage: decode %s: %w", path, err)
	}
	if doc.Reached == nil {
		doc.Reached = map[string]string{}
	}
	return doc, nil
}

// checkNotBeforeCurrent refuses a stage reached before the current one.
func checkNotBeforeCurrent(doc stageDoc, stage string, when time.Time) error {
	if doc.Stage == "" {
		return nil
	}
	current, err := time.Parse(time.RFC3339, doc.Reached[doc.Stage])
	if err != nil {
		return fmt.Errorf("stage: %s of %s: %w", doc.Stage, doc.SubjectID, err)
	}
	if when.Before(current) {
		return fmt.Errorf("stage: %s at %s is before the current stage %s at %s",
			stage, when.Format(time.RFC3339), doc.Stage, current.Format(time.RFC3339))
	}
	return nil
}

func writeStage(ctx context.Context, docs ports.Docs, index ports.Index, path string, doc stageDoc, when time.Time) error {
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("stage: encode: %w", err)
	}
	fields := map[string]string{
		"subject_id":  doc.SubjectID,
		"stage":       doc.Stage,
		"declared_by": doc.DeclaredBy,
	}
	for stage, at := range doc.Reached {
		fields[stage+"_at"] = at
	}
	_, err = RecordDocument(ctx, docs, index, Document{
		Path:    path,
		Body:    body,
		Message: "Declare " + doc.Stage + " for " + doc.SubjectID,
		Kind:    "stage",
		Fields:  fields,
		When:    when,
	})
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	return nil
}

// CurrentStage returns the stage subjectID last reached, or "" when none. It
// relies on the index keeping one row per path.
func CurrentStage(ctx context.Context, index ports.Index, subjectID string) (string, error) {
	rows, err := index.Find(ctx, ports.Query{Kind: "stage", Match: map[string]string{"subject_id": subjectID}, Limit: 1})
	if err != nil {
		return "", fmt.Errorf("stage: find %s: %w", subjectID, err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Fields["stage"], nil
}

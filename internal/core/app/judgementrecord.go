package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// judgementTimeFormat is RFC 3339 with colons replaced by dashes, since a
// colon is not portable in a path component.
const judgementTimeFormat = "2006-01-02T15-04-05Z"

// judgementQuestion is a Question as it appears in a judgement document.
type judgementQuestion struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Ask     string   `json:"ask"`
	Options []string `json:"options,omitempty"`
}

// judgementAnswer is an Answer as it appears in a judgement document.
type judgementAnswer struct {
	ID           string             `json:"id"`
	Kind         string             `json:"kind"`
	Chosen       string             `json:"chosen"`
	Distribution map[string]float64 `json:"distribution"`
	Expected     float64            `json:"expected,omitempty"`
}

// judgementDoc is a judgement as it is written to the record. Outcome is
// carried as `any` and never omitted, so the key reads null until a later
// increment fills it in.
type judgementDoc struct {
	SubjectID string              `json:"subject_id"`
	Subject   string              `json:"subject"`
	Model     string              `json:"model"`
	When      string              `json:"when"`
	Questions []judgementQuestion `json:"questions"`
	Answers   []judgementAnswer   `json:"answers"`
	Outcome   any                 `json:"outcome"`
}

// RecordJudgement writes j as a document under the subject it judged and
// indexes it as pending. It returns the path written.
//
// Git holds the judgement; the index row only helps find it. A failed
// Upsert after a successful Put still returns the path, since the document
// is already recorded and the index can be rebuilt from it.
func RecordJudgement(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, qs []ports.Question, j ports.Judgement) (string, error) {
	if subjectID == "" {
		return "", errors.New("judgement: subject id is empty")
	}
	if len(j.Answers) == 0 {
		return "", errors.New("judgement: no answers to record")
	}

	path := judgementPath(subjectID, j)
	body, err := json.MarshalIndent(judgementDocFor(subjectID, qs, j), "", "  ")
	if err != nil {
		return "", fmt.Errorf("judgement: encode: %w", err)
	}

	rev, err := docs.Put(ctx, path, body, "Record judgement for "+subjectID)
	if err != nil {
		return "", fmt.Errorf("judgement: put: %w", err)
	}

	row := judgementRow(path, rev, subjectID, qs, j)
	if err := index.Upsert(ctx, row); err != nil {
		return path, fmt.Errorf("judgement: upsert %s: %w", path, err)
	}

	return path, nil
}

// judgementPath is where a judgement of subjectID at j.When is written.
// Keying by subject and timestamp means judging the same subject twice
// never overwrites the first.
func judgementPath(subjectID string, j ports.Judgement) string {
	return fmt.Sprintf("judgements/%s/%s.json", subjectID, j.When.UTC().Format(judgementTimeFormat))
}

// judgementDocFor builds the document body for a judgement of subjectID.
func judgementDocFor(subjectID string, qs []ports.Question, j ports.Judgement) judgementDoc {
	questions := make([]judgementQuestion, len(qs))
	for i, q := range qs {
		questions[i] = judgementQuestion{ID: q.ID, Kind: string(q.Kind), Ask: q.Ask, Options: q.Options}
	}

	answers := make([]judgementAnswer, len(j.Answers))
	for i, a := range j.Answers {
		answers[i] = judgementAnswer{
			ID:           a.ID,
			Kind:         string(a.Kind),
			Chosen:       a.Chosen,
			Distribution: a.Distribution,
			Expected:     a.Expected,
		}
	}

	return judgementDoc{
		SubjectID: subjectID,
		Subject:   j.Subject,
		Model:     j.Model,
		When:      j.When.UTC().Format(time.RFC3339),
		Questions: questions,
		Answers:   answers,
		Outcome:   nil,
	}
}

// judgementRow is the index row for a judgement written at path and rev.
// Fields are flat strings; the distribution stays in the document alone.
func judgementRow(path string, rev ports.Revision, subjectID string, qs []ports.Question, j ports.Judgement) ports.Record {
	return ports.Record{
		Path: path,
		Rev:  rev,
		Kind: "judgement",
		When: j.When,
		Fields: map[string]string{
			"subject_id": subjectID,
			"model":      j.Model,
			"questions":  questionIDs(qs),
			"outcome":    "pending",
		},
	}
}

// questionIDs joins qs's ids with a comma, in order.
func questionIDs(qs []ports.Question) string {
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	return strings.Join(ids, ",")
}

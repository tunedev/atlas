package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// judgementQuestion is a Question as it appears in a judgement document.
type judgementQuestion struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Ask     string   `json:"ask"`
	Options []string `json:"options,omitempty"`
}

// judgementAlternative is one alternative the engine returned at an answer
// token, as ports.Alternative appears in a judgement document.
type judgementAlternative struct {
	Text    string  `json:"text"`
	LogProb float64 `json:"log_prob"`
}

// judgementAnswer is an Answer as it appears in a judgement document.
// Expected carries no omitempty: a score of exactly 0 (all mass on the
// first level) is a real answer, not an absent one.
type judgementAnswer struct {
	ID           string                 `json:"id"`
	Kind         string                 `json:"kind"`
	Chosen       string                 `json:"chosen"`
	Distribution map[string]float64     `json:"distribution"`
	Expected     float64                `json:"expected"`
	Alternatives []judgementAlternative `json:"alternatives"`
	Confidence   float64                `json:"confidence"`
	Coverage     judgementCoverage      `json:"coverage"`
}

// judgementCoverage is ports.Coverage as it appears in a judgement document.
type judgementCoverage struct {
	Represented int `json:"represented"`
	Declared    int `json:"declared"`
}

// judgementSampling is ports.Sampling as it appears in a judgement document.
type judgementSampling struct {
	Temperature float64 `json:"temperature"`
	Seed        int     `json:"seed"`
	TopLogProbs int     `json:"top_logprobs"`
	MaxTokens   int     `json:"max_tokens"`
}

// judgementDoc is a judgement as it is written to the record. Outcome is
// carried as `any` and never omitted, so the key reads null until a later
// increment fills it in. Provider and Sampling record what produced the
// probability: they cannot be added retroactively to a judgement already
// made.
type judgementDoc struct {
	SubjectID string              `json:"subject_id"`
	Subject   string              `json:"subject"`
	Model     string              `json:"model"`
	Provider  string              `json:"provider"`
	Sampling  judgementSampling   `json:"sampling"`
	When      string              `json:"when"`
	Questions []judgementQuestion `json:"questions"`
	Answers   []judgementAnswer   `json:"answers"`

	Fingerprint string       `json:"fingerprint,omitempty"`
	Rules       []RuleResult `json:"rules,omitempty"`

	Outcome any `json:"outcome"`
}

// Assessed is what a board run adds to a judgement: the fingerprint it is
// found by, the verdict it was assessed to, and the rules checked against
// its subject.
type Assessed struct {
	Fingerprint string
	Verdict     string
	Rules       []RuleResult
}

// RecordJudgement writes j as a document under the subject it judged and
// indexes it as pending. It returns the path written.
//
// Git holds the judgement; the index row only helps find it. A failed
// Upsert after a successful Put still returns the path, since the document
// is already recorded and the index can be rebuilt from it.
func RecordJudgement(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, qs []ports.Question, j ports.Judgement) (string, error) {
	return RecordAssessedJudgement(ctx, docs, index, subjectID, qs, j, Assessed{})
}

// RecordAssessedJudgement writes j with a's fingerprint, verdict and rule
// results, and indexes it under all three so a later run can find it by
// fingerprint and a reader can list judgements by verdict or tripped rule.
// With a zero Assessed it records exactly what RecordJudgement always has.
func RecordAssessedJudgement(ctx context.Context, docs ports.Docs, index ports.Index, subjectID string, qs []ports.Question, j ports.Judgement, a Assessed) (string, error) {
	if subjectID == "" {
		return "", errors.New("judgement: subject id is empty")
	}
	if err := CheckSubjectID(subjectID); err != nil {
		return "", fmt.Errorf("judgement: %w", err)
	}
	if len(j.Answers) == 0 {
		return "", errors.New("judgement: no answers to record")
	}

	path := judgementPath(subjectID, j)
	body, err := json.MarshalIndent(judgementDocFor(subjectID, qs, j, a), "", "  ")
	if err != nil {
		return "", fmt.Errorf("judgement: encode: %w", err)
	}

	rev, err := RecordDocument(ctx, docs, index, Document{
		Path:    path,
		Body:    body,
		Message: "Record judgement for " + subjectID,
		Kind:    "judgement",
		Fields:  judgementFields(subjectID, qs, j, a),
		When:    j.When,
	})
	if err != nil {
		if rev == "" {
			return "", fmt.Errorf("judgement: %w", err)
		}
		return path, fmt.Errorf("judgement: %w", err)
	}
	return path, nil
}

// judgementPath is where a judgement of subjectID at j.When is written.
// Keying by subject and millisecond timestamp means two judgements of the
// same subject collide only if they carry the same When to the millisecond;
// the real Judge stamps When from time.Now(), so a caller passing distinct
// timestamps gets distinct paths.
func judgementPath(subjectID string, j ports.Judgement) string {
	return fmt.Sprintf("judgements/%s/%s.json", subjectID, j.When.UTC().Format(recordTimeFormat))
}

// judgementDocFor builds the document body for a judgement of subjectID.
func judgementDocFor(subjectID string, qs []ports.Question, j ports.Judgement, a Assessed) judgementDoc {
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
			Alternatives: judgementAlternatives(a.Alternatives),
			Confidence:   a.Confidence,
			Coverage:     judgementCoverage{Represented: a.Coverage.Represented, Declared: a.Coverage.Declared},
		}
	}

	return judgementDoc{
		SubjectID: subjectID,
		Subject:   j.Subject,
		Model:     j.Model,
		Provider:  j.Provider,
		Sampling: judgementSampling{
			Temperature: j.Sampling.Temperature,
			Seed:        j.Sampling.Seed,
			TopLogProbs: j.Sampling.TopLogProbs,
			MaxTokens:   j.Sampling.MaxTokens,
		},
		When:        j.When.UTC().Format(recordDocTimeFormat),
		Questions:   questions,
		Answers:     answers,
		Fingerprint: a.Fingerprint,
		Rules:       a.Rules,
		Outcome:     nil,
	}
}

// judgementAlternatives converts alts to how they appear in a judgement
// document.
func judgementAlternatives(alts []ports.Alternative) []judgementAlternative {
	out := make([]judgementAlternative, len(alts))
	for i, a := range alts {
		out[i] = judgementAlternative{Text: a.Text, LogProb: a.LogProb}
	}
	return out
}

// judgementFields is the flat index fields for a judgement; the
// distribution stays in the document alone.
func judgementFields(subjectID string, qs []ports.Question, j ports.Judgement, a Assessed) map[string]string {
	fields := map[string]string{
		"subject_id": subjectID,
		"model":      j.Model,
		"provider":   j.Provider,
		"questions":  questionIDs(qs),
		"outcome":    "pending",
	}
	if a.Fingerprint != "" {
		fields["fingerprint"] = a.Fingerprint
		fields["verdict"] = a.Verdict
		fields["tripped"] = trippedIDs(a.Rules)
	}
	return fields
}

// questionIDs joins qs's ids with a comma, in order.
func questionIDs(qs []ports.Question) string {
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	return strings.Join(ids, ",")
}

// trippedIDs joins the ids of the tripped rules with a comma, in order.
func trippedIDs(results []RuleResult) string {
	var ids []string
	for _, r := range results {
		if r.State == RuleTripped {
			ids = append(ids, r.ID)
		}
	}
	return strings.Join(ids, ",")
}

// StoredJudgement is a recorded judgement as a later run reuses it: whom it
// was about, its answers, and the rules it was checked against.
type StoredJudgement struct {
	SubjectID string
	Answers   []ports.Answer
	Rules     []RuleResult
}

// ReadJudgement reads the judgement document at path back into answers and
// rule results.
func ReadJudgement(ctx context.Context, docs ports.Docs, path string) (StoredJudgement, error) {
	body, err := docs.Get(ctx, path)
	if err != nil {
		return StoredJudgement{}, fmt.Errorf("judgement: read %s: %w", path, err)
	}
	var doc judgementDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return StoredJudgement{}, fmt.Errorf("judgement: decode %s: %w", path, err)
	}
	answers := make([]ports.Answer, len(doc.Answers))
	for i, a := range doc.Answers {
		answers[i] = ports.Answer{
			ID:           a.ID,
			Kind:         ports.Kind(a.Kind),
			Chosen:       a.Chosen,
			Distribution: a.Distribution,
			Expected:     a.Expected,
			Confidence:   a.Confidence,
			Coverage:     ports.Coverage{Represented: a.Coverage.Represented, Declared: a.Coverage.Declared},
		}
	}
	return StoredJudgement{SubjectID: doc.SubjectID, Answers: answers, Rules: doc.Rules}, nil
}

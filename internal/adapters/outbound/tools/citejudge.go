package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// CitationsJudge asks a Judge, once per claim, whether each grounded quote
// cited for it directly shows it, and marks every citation relevant or not.
// A claim is an object with a non-empty "text" and "citations". A citation
// that is not grounded is never relevant and is not asked about.
type CitationsJudge struct {
	judge ports.Judge
	docs  ports.Docs
	index ports.Index
}

func NewCitationsJudge(j ports.Judge, docs ports.Docs, index ports.Index) *CitationsJudge {
	return &CitationsJudge{judge: j, docs: docs, index: index}
}

func (c *CitationsJudge) Name() string { return "citations.judge" }

func (c *CitationsJudge) Invoke(ctx context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("citations.judge: fields: %w", err)
	}
	threshold, err := strconv.ParseFloat(with["threshold"], 64)
	if err != nil || math.IsNaN(threshold) || threshold <= 0 || threshold > 1 {
		return nil, fmt.Errorf("citations.judge: threshold must be greater than 0 and at most 1, got %q", with["threshold"])
	}
	subjectID := with["subject_id"]
	if subjectID == "" {
		return nil, fmt.Errorf("citations.judge: no subject id")
	}

	judged := 0
	var walkErr error
	walkClaims(tree, func(text string, citations []map[string]any) {
		if walkErr != nil {
			return
		}
		asked, qs := questionsFor(citations)
		if len(qs) == 0 {
			return
		}
		j, err := c.judge.Ask(ctx, text, qs)
		if err != nil {
			walkErr = err
			return
		}
		if err := markRelevance(asked, j, threshold); err != nil {
			walkErr = err
			return
		}
		if _, err := app.RecordJudgement(ctx, c.docs, c.index, fmt.Sprintf("%s-claim-%d", subjectID, judged), qs, j); err != nil {
			walkErr = err
			return
		}
		judged++
	})
	if walkErr != nil {
		return nil, fmt.Errorf("citations.judge: %w", walkErr)
	}
	return map[string]any{"fields": tree, "judged": judged}, nil
}

// walkClaims calls fn for every object with a non-empty string "text" and a
// "citations" list, in a fixed order, after marking every citation not
// relevant; questionsFor and markRelevance then set the ones judged.
func walkClaims(v any, fn func(string, []map[string]any)) {
	switch val := v.(type) {
	case map[string]any:
		text, _ := val["text"].(string)
		if raw, ok := val["citations"].([]any); ok && text != "" {
			cites := make([]map[string]any, 0, len(raw))
			for _, r := range raw {
				if m, ok := r.(map[string]any); ok {
					m["relevant"] = false
					cites = append(cites, m)
				}
			}
			fn(text, cites)
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkClaims(val[k], fn)
		}
	case []any:
		for _, e := range val {
			walkClaims(e, fn)
		}
	}
}

// questionsFor builds one noul question per grounded citation, returning the
// citations asked about in question order.
func questionsFor(citations []map[string]any) ([]map[string]any, []ports.Question) {
	var asked []map[string]any
	var qs []ports.Question
	for _, c := range citations {
		quote, _ := c["quote"].(string)
		if c["status"] != "grounded" || quote == "" {
			continue
		}
		asked = append(asked, c)
		qs = append(qs, ports.Question{
			ID:      "c" + strconv.Itoa(len(qs)),
			Kind:    ports.KindNoul,
			Ask:     "Evidence: " + quote + "\nDoes this evidence, on its own, directly show that the subject is true?",
			Options: ports.NoulOptions(),
			Forms:   ports.NoulForms(),
		})
	}
	return asked, qs
}

// markRelevance sets relevant and p on each asked citation from j. It is an
// error for a question to have no matching answer in j: the caller must
// never see a citation silently treated as not relevant for that reason.
func markRelevance(asked []map[string]any, j ports.Judgement, threshold float64) error {
	byID := make(map[string]ports.Answer, len(j.Answers))
	for _, a := range j.Answers {
		byID[a.ID] = a
	}
	for i, c := range asked {
		id := "c" + strconv.Itoa(i)
		a, ok := byID[id]
		if !ok {
			return fmt.Errorf("no answer for %s", id)
		}
		p := a.Distribution["yes"]
		c["p"] = p
		c["relevant"] = p >= threshold
	}
	return nil
}

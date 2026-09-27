package app

import (
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Assessment is a verdict with the reasons behind it. Verdict and P are the
// verdict question's own answer: rules add reasons and never change it.
type Assessment struct {
	Verdict string
	P       float64
	Reasons []string
	Rules   []RuleResult
}

// Assess builds an Assessment from one judgement's answers and the rules
// checked against the same subject. Reasons list tripped rules, then
// unknown rules, then a line for the verdict itself when its coverage is
// zero, then every other answer (a rule's own question is reported through
// its rule), then clear rules. An answer whose coverage is zero says so,
// since its probability then measured nothing.
func Assess(verdictID string, answers []ports.Answer, results []RuleResult) (Assessment, error) {
	var verdict *ports.Answer
	var others []string
	for i, a := range answers {
		switch {
		case a.ID == verdictID:
			verdict = &answers[i]
		case strings.HasPrefix(a.ID, ruleQuestionPrefix):
		default:
			others = append(others, answerReason(a))
		}
	}
	if verdict == nil {
		return Assessment{}, fmt.Errorf("assess: no answer to verdict question %q", verdictID)
	}

	reasons := ruleReasons(results, RuleTripped)
	reasons = append(reasons, ruleReasons(results, RuleUnknown)...)
	if verdict.Coverage.Declared > 0 && verdict.Coverage.Represented == 0 {
		reasons = append(reasons, fmt.Sprintf("verdict: %s (%.2f, coverage 0 of %d)",
			verdict.Chosen, verdict.Distribution[verdict.Chosen], verdict.Coverage.Declared))
	}
	reasons = append(reasons, others...)
	reasons = append(reasons, ruleReasons(results, RuleClear)...)
	return Assessment{
		Verdict: verdict.Chosen,
		P:       verdict.Distribution[verdict.Chosen],
		Reasons: reasons,
		Rules:   results,
	}, nil
}

func answerReason(a ports.Answer) string {
	s := fmt.Sprintf("%s: %s (%.2f", a.ID, a.Chosen, a.Distribution[a.Chosen])
	if a.Coverage.Declared > 0 && a.Coverage.Represented == 0 {
		s += fmt.Sprintf(", coverage 0 of %d", a.Coverage.Declared)
	}
	return s + ")"
}

func ruleReasons(results []RuleResult, state string) []string {
	var out []string
	for _, r := range results {
		if r.State == state {
			out = append(out, fmt.Sprintf("%s %s: %s", r.ID, r.State, r.Evidence))
		}
	}
	return out
}

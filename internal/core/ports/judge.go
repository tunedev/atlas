package ports

import (
	"context"
	"time"
)

// Kind is what shape an answer takes.
type Kind string

const (
	// KindNoul is a probability of yes.
	KindNoul Kind = "noul"
	// KindChoice is one option, with the distribution over all of them.
	KindChoice Kind = "choice"
	// KindScore is a probability-weighted position on ordered levels.
	KindScore Kind = "score"
)

// Question is one typed question. Options are the allowed answers: the options
// themselves for a choice, the ordered levels for a score, and yes and no for a
// noul. Forms names extra surface forms that count as an option, so that Yes
// counts as yes.
type Question struct {
	ID      string
	Kind    Kind
	Ask     string
	Options []string
	Forms   map[string][]string
}

// Answer is what a model answered and how much probability mass each option
// held. Distribution sums to one. Expected is set for a score alone: the
// levels' positions weighted by their mass.
type Answer struct {
	ID           string
	Kind         Kind
	Chosen       string
	Distribution map[string]float64
	Expected     float64
}

// Judgement is every answer about one subject, and which model produced them.
type Judgement struct {
	Subject string
	Model   string
	When    time.Time
	Answers []Answer
}

// Judge answers typed questions about a subject. Every question is answered in
// one call, so the answers cannot contradict each other.
type Judge interface {
	Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)
}

// NoulOptions is the option list a noul is asked with.
func NoulOptions() []string { return []string{"yes", "no"} }

// NoulForms is the surface forms that count as each noul option.
func NoulForms() map[string][]string {
	return map[string][]string{
		"yes": {"yes", "Yes", "YES", "true"},
		"no":  {"no", "No", "NO", "false"},
	}
}

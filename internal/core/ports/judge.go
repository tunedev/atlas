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
// levels' positions weighted by their mass. Alternatives is the raw
// alternatives the engine returned at the answer token, before any option
// matching or normalising, so a distribution built from one alternative can
// be told apart from one built from several.
//
// Confidence and Coverage both describe how much the distribution is worth
// trusting, and neither can substitute for the other. Confidence is 1 minus
// Distribution's normalised entropy: it reads 1 whenever all mass sits on
// one option, whether that option won by beating real rivals or because it
// was the only option present at all. Coverage.Represented says which of
// those two happened: how many of the question's declared options the
// engine's alternatives actually named. A Confidence of 1 with a
// Coverage.Represented of 0 is not a contradiction -- it is the single-option
// fallback made visible.
type Answer struct {
	ID           string
	Kind         Kind
	Chosen       string
	Distribution map[string]float64
	Expected     float64
	Alternatives []Alternative
	Confidence   float64
	Coverage     Coverage
}

// Coverage says how many of a question's declared options the engine's
// alternatives actually named (Represented), out of how many it had
// (Declared). It counts only what appeared among the alternatives: an
// option that entered Distribution solely through the own-text fallback
// (rawMassPerClass in app/mass.go) is not represented, since the point of
// Coverage is to show when the engine offered nothing about the question's
// options at all.
type Coverage struct {
	Represented int
	Declared    int
}

// Sampling is how the engine was told to sample when it produced a
// Judgement. A probability read under unknown sampling is not a calibrated
// number, so it travels with the judgement rather than living only in
// config: config can change before the document is read again.
type Sampling struct {
	Temperature float64
	Seed        int
	TopLogProbs int
	MaxTokens   int
}

// Judgement is every answer about one subject, which model produced them,
// through which provider, and under what sampling.
type Judgement struct {
	Subject  string
	Model    string
	Provider string
	Sampling Sampling
	When     time.Time
	Answers  []Answer
}

// Judge answers typed questions about a subject. Every question is answered in
// one call, so the answers cannot contradict each other.
type Judge interface {
	Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)
}

// NoulOptions is the option list a noul is asked with.
func NoulOptions() []string { return []string{"yes", "no"} }

// NoulForms is the surface forms that count as each noul option. It
// includes "true"/"false" alongside "yes"/"no" even though AnswerSchema
// constrains the emitted field to an enum of "yes" and "no" alone, so a
// constrained engine can never choose "true" or "false" as its answer:
// Alternatives at a token carry the model's pre-constraint distribution
// (docs/specs/2026-09-22-judge-design.md), and a model reasoning about a
// yes/no question can still surface "true"/"false" among the alternatives
// it did not choose. Keeping the forms lets that mass count toward the
// right option instead of going unmatched.
func NoulForms() map[string][]string {
	return map[string][]string{
		"yes": {"yes", "Yes", "YES", "true"},
		"no":  {"no", "No", "NO", "false"},
	}
}

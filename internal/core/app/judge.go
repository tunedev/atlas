package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// judgeSystemMessage tells the model that every field must hold exactly one
// of the values the schema allows it, and nothing else.
const judgeSystemMessage = "Answer every field in the schema. Each field's value must be exactly one of its allowed options, and nothing else."

// JudgeConfig pins how a Judge samples: a fixed temperature and seed so
// repeated runs over the same subject answer the same way, TopLogProbs many
// alternatives per token to sum mass from, and MaxTokens bounding the reply.
type JudgeConfig struct {
	Temperature float64
	Seed        int
	TopLogProbs int
	MaxTokens   int
}

// Judge answers every question about a subject in one call to a Provider,
// reading each answer's probability mass from the completion's alternatives.
type Judge struct {
	provider ports.Provider
	cfg      JudgeConfig
}

// NewJudge builds a Judge that asks p, sampling as cfg directs.
func NewJudge(p ports.Provider, cfg JudgeConfig) *Judge {
	return &Judge{provider: p, cfg: cfg}
}

// Ask answers every question in qs about subject with a single call to the
// provider, then reads each answer's mass from the completion's per-token
// alternatives.
func (j *Judge) Ask(ctx context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	schema, err := AnswerSchema(qs)
	if err != nil {
		return ports.Judgement{}, err
	}

	completion, err := j.provider.Complete(ctx, j.promptFor(subject, qs, schema))
	if err != nil {
		return ports.Judgement{}, fmt.Errorf("judge: %w", err)
	}

	tokens, err := AnswerTokens(completion, idsOf(qs))
	if err != nil {
		return ports.Judgement{}, err
	}

	answers, err := answersFor(qs, tokens)
	if err != nil {
		return ports.Judgement{}, err
	}

	return ports.Judgement{Subject: subject, Model: completion.Model, When: time.Now().UTC(), Answers: answers}, nil
}

// promptFor builds the request for qs about subject: the schema constrains
// the reply, TopLogProbs asks for the alternatives mass is read from, and
// temperature and seed are pinned from cfg.
func (j *Judge) promptFor(subject string, qs []ports.Question, schema []byte) ports.Prompt {
	temperature := j.cfg.Temperature
	seed := j.cfg.Seed
	return ports.Prompt{
		System:      judgeSystemMessage,
		User:        promptUserMessage(subject, qs),
		MaxTokens:   j.cfg.MaxTokens,
		Schema:      schema,
		TopLogProbs: j.cfg.TopLogProbs,
		Temperature: &temperature,
		Seed:        &seed,
	}
}

// promptUserMessage names the subject, then every question's id and text.
func promptUserMessage(subject string, qs []ports.Question) string {
	var sb strings.Builder
	sb.WriteString("Subject: ")
	sb.WriteString(subject)
	sb.WriteString("\n\n")
	for _, q := range qs {
		sb.WriteString(q.ID)
		sb.WriteString(": ")
		sb.WriteString(q.Ask)
		sb.WriteString("\n")
	}
	return sb.String()
}

// idsOf returns qs's question ids, in order.
func idsOf(qs []ports.Question) []string {
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	return ids
}

// answersFor reads one ports.Answer per question in qs from tokens, keyed by
// question id.
func answersFor(qs []ports.Question, tokens map[string]ports.Token) ([]ports.Answer, error) {
	answers := make([]ports.Answer, 0, len(qs))
	for _, q := range qs {
		answer, err := answerFor(q, tokens[q.ID])
		if err != nil {
			return nil, err
		}
		answers = append(answers, answer)
	}
	return answers, nil
}

// answerFor reads one question's answer from the token that carries it: the
// probability mass over its options, the option holding the most of it, and,
// for a score, the mass-weighted position.
//
// A token with no alternatives is an error rather than a distribution: with
// nothing to sum, reporting one option at full mass would invent a number
// the completion never supported.
func answerFor(q ports.Question, tok ports.Token) (ports.Answer, error) {
	if len(tok.Alternatives) == 0 {
		return ports.Answer{}, fmt.Errorf("judge: %s: answer token carries no alternatives to sum mass from", q.ID)
	}

	options := OptionsFor(q)
	mass, err := massForToken(q.ID, tok, classesFor(q, options))
	if err != nil {
		return ports.Answer{}, err
	}

	answer := ports.Answer{
		ID:           q.ID,
		Kind:         q.Kind,
		Chosen:       chosenOption(options, mass),
		Distribution: mass,
	}
	if q.Kind == ports.KindScore {
		answer.Expected = expectedPosition(options, mass)
	}
	return answer, nil
}

// classesFor maps each of options to its surface forms: q.Forms's entry for
// that option, or the option itself when it names none. A noul with no forms
// of its own falls back to ports.NoulForms, so "Yes" and "yes" count as one
// answer.
func classesFor(q ports.Question, options []string) map[string][]string {
	forms := q.Forms
	if len(forms) == 0 && q.Kind == ports.KindNoul {
		forms = ports.NoulForms()
	}

	classes := make(map[string][]string, len(options))
	for _, o := range options {
		if fs := forms[o]; len(fs) > 0 {
			classes[o] = fs
		} else {
			classes[o] = []string{o}
		}
	}
	return classes
}

// massForToken reads the probability mass tok assigns to each option in
// classes, matching an alternative's text by optionMatch rather than by
// whole-string equality: a real engine tokenises a multi-word or
// multi-syllable option, so the answer token's own text is often only that
// option's first token, and only its own class distinguishes it from the
// others.
//
// Tok's own probability is folded in only when its own text is absent from
// its alternatives, the same double-count guard MassAtToken applies -- an
// OpenAI-compatible top_logprobs array already contains the chosen token.
// The result is normalised so the values sum to one.
func massForToken(qid string, tok ports.Token, classes map[string][]string) (map[string]float64, error) {
	raw := make(map[string]float64)
	ownTextSeen := false
	for _, alt := range tok.Alternatives {
		option, err := optionMatch(qid, alt.Text, classes)
		if err != nil {
			return nil, err
		}
		if option != "" {
			raw[option] += math.Exp(alt.LogProb)
		}
		if alt.Text == tok.Text {
			ownTextSeen = true
		}
	}
	if !ownTextSeen {
		option, err := optionMatch(qid, tok.Text, classes)
		if err != nil {
			return nil, err
		}
		if option != "" {
			raw[option] += math.Exp(tok.LogProb)
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("judge: %s: no token matches any option", qid)
	}
	return normalise(raw), nil
}

// optionMatch resolves text to the option in classes it identifies. Text is
// trimmed of surrounding whitespace and quotes first. An exact match against
// one of an option's forms wins outright; otherwise a non-empty prefix of a
// form counts, provided it prefixes only one option's forms. It returns ""
// when trimmed matches no option, and an error when trimmed is a
// non-distinguishing prefix shared by two or more different options.
func optionMatch(qid, text string, classes map[string][]string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(text), `"`)
	if trimmed == "" {
		return "", nil
	}
	if option := exactOptionMatch(trimmed, classes); option != "" {
		return option, nil
	}
	return prefixOptionMatch(qid, trimmed, classes)
}

// exactOptionMatch returns the option whose forms contain trimmed exactly,
// or "" if none does.
func exactOptionMatch(trimmed string, classes map[string][]string) string {
	for option, forms := range classes {
		for _, form := range forms {
			if form == trimmed {
				return option
			}
		}
	}
	return ""
}

// prefixOptionMatch returns the option that trimmed is a non-empty prefix
// of exactly one of, or an error naming qid and trimmed when it prefixes two
// or more different options.
func prefixOptionMatch(qid, trimmed string, classes map[string][]string) (string, error) {
	matched := ""
	for option, forms := range classes {
		if matched == option {
			continue
		}
		for _, form := range forms {
			if strings.HasPrefix(form, trimmed) {
				if matched != "" {
					return "", fmt.Errorf("judge: %s: %q is ambiguous between %s and %s", qid, trimmed, matched, option)
				}
				matched = option
				break
			}
		}
	}
	return matched, nil
}

// chosenOption returns the option in options holding the most mass.
func chosenOption(options []string, mass map[string]float64) string {
	best := ""
	bestMass := -1.0
	for _, o := range options {
		if m := mass[o]; m > bestMass {
			bestMass = m
			best = o
		}
	}
	return best
}

// expectedPosition sums each option's index in options, weighted by its
// mass: a score's probability-weighted position on its ordered levels.
func expectedPosition(options []string, mass map[string]float64) float64 {
	var expected float64
	for i, o := range options {
		expected += float64(i) * mass[o]
	}
	return expected
}

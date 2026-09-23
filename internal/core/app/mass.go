package app

import (
	"fmt"
	"math"

	"github.com/tunedev/atlas/internal/core/ports"
)

// MassPerClass reads the probability mass a completion assigns to each class
// in classes, a map from class name to the surface forms that count as that
// class (for example "yes" to {"yes", "Yes", "YES", "true"}).
//
// The answer-bearing token is the last token in the completion whose own
// text belongs to a class. A schema can force a structural token, such as
// "{", ahead of the answer; its alternatives may still look like classes
// because the model intended prose before the schema intervened, but its own
// text is not a class member, so it is skipped. Mass is then read from that
// token with MassAtToken.
//
// An empty Tokens slice is an error: per-token data was not requested.
func MassPerClass(c ports.Completion, classes map[string][]string) (map[string]float64, error) {
	if len(c.Tokens) == 0 {
		return nil, fmt.Errorf("mass: completion carries no per-token data")
	}

	tok, ok := lastClassBearingToken(c.Tokens, classes)
	if !ok {
		return nil, fmt.Errorf("mass: no token matches any class")
	}

	return MassAtToken(tok, classes)
}

// matcher resolves text to the class or option in classes it identifies, or
// "" when none does. It may report an error when text cannot be resolved
// unambiguously.
type matcher func(text string, classes map[string][]string) (string, error)

// exactMatch is the matcher MassAtToken uses by default: it matches text
// against a class's surface forms by equality alone, and never errors.
func exactMatch(text string, classes map[string][]string) (string, error) {
	return classOf(text, classes), nil
}

// MassAtToken reads the probability mass tok assigns to each class in
// classes, a map from class name to the surface forms that count as that
// class (for example "yes" to {"yes", "Yes", "YES", "true"}).
//
// Mass for a class is the sum of math.Exp(logprob) over every one of tok's
// alternatives whose text match resolves to that class, plus tok's own
// probability when its own text is absent from its alternatives -- an
// OpenAI-compatible top_logprobs array already contains the chosen token, so
// adding it again would double-count that surface form. The result is
// normalised so the values sum to one.
//
// match, when given, decides what text identifies which class; omitting it
// (or passing nil) matches by exact equality, which is what every caller
// used before match existed. app.Judge passes a matcher of its own: exact,
// then an unambiguous prefix, so a multi-token option's first token can
// still be told apart from another's.
//
// A token where neither its own text nor any alternative matches a class is
// an error, since a uniform distribution there would be an invented number.
// match returning its own error (an ambiguous match, for instance) is
// reported too, naming which text -- an alternative or the emitted token --
// and its probability.
func MassAtToken(tok ports.Token, classes map[string][]string, match ...matcher) (map[string]float64, error) {
	m := matcher(exactMatch)
	if len(match) > 0 && match[0] != nil {
		m = match[0]
	}

	raw, err := rawMassPerClass(tok, classes, m)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("mass: no token matches any class")
	}
	return normalise(raw), nil
}

// lastClassBearingToken returns the last token whose own text belongs to a
// class, and whether one was found.
func lastClassBearingToken(tokens []ports.Token, classes map[string][]string) (ports.Token, bool) {
	for i := len(tokens) - 1; i >= 0; i-- {
		if classOf(tokens[i].Text, classes) != "" {
			return tokens[i], true
		}
	}
	return ports.Token{}, false
}

// classOf returns the class whose surface forms contain text exactly, or ""
// if none does.
func classOf(text string, classes map[string][]string) string {
	for class, forms := range classes {
		for _, form := range forms {
			if form == text {
				return class
			}
		}
	}
	return ""
}

// rawMassPerClass sums math.Exp(logprob) per class over tok's alternatives,
// resolving each alternative's text (and, when unseen among them, tok's own
// text) to a class with match. A class of "" is not summed.
func rawMassPerClass(tok ports.Token, classes map[string][]string, match matcher) (map[string]float64, error) {
	mass := make(map[string]float64)
	ownTextSeen := false
	for _, alt := range tok.Alternatives {
		class, err := match(alt.Text, classes)
		if err != nil {
			return nil, fmt.Errorf("%w (alternative %q, p=%.4f)", err, alt.Text, math.Exp(alt.LogProb))
		}
		if class != "" {
			mass[class] += math.Exp(alt.LogProb)
		}
		if alt.Text == tok.Text {
			ownTextSeen = true
		}
	}
	if !ownTextSeen {
		class, err := match(tok.Text, classes)
		if err != nil {
			return nil, fmt.Errorf("%w (emitted token %q, p=%.4f)", err, tok.Text, math.Exp(tok.LogProb))
		}
		if class != "" {
			mass[class] += math.Exp(tok.LogProb)
		}
	}
	return mass, nil
}

// normalise scales mass so its values sum to one.
func normalise(mass map[string]float64) map[string]float64 {
	total := 0.0
	for _, v := range mass {
		total += v
	}
	out := make(map[string]float64, len(mass))
	for class, v := range mass {
		out[class] = v / total
	}
	return out
}

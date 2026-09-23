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
// text is not a class member, so it is skipped.
//
// At the answer-bearing token, mass for a class is the sum of
// math.Exp(logprob) over every alternative whose text matches one of the
// class's surface forms, compared case-sensitively. The token's own
// probability is included only when its own text is absent from its
// alternatives -- an OpenAI-compatible top_logprobs array already contains
// the chosen token, so adding it again would double-count that surface form.
// The result is normalised so the values sum to one.
//
// An empty Tokens slice is an error: per-token data was not requested. A
// completion where no token's text matches any class is an error too, since
// a uniform distribution there would be an invented number.
func MassPerClass(c ports.Completion, classes map[string][]string) (map[string]float64, error) {
	if len(c.Tokens) == 0 {
		return nil, fmt.Errorf("mass: completion carries no per-token data")
	}

	tok, ok := lastClassBearingToken(c.Tokens, classes)
	if !ok {
		return nil, fmt.Errorf("mass: no token matches any class")
	}

	raw := rawMassPerClass(tok, classes)
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

// classOf returns the class whose surface forms contain text, or "" if none
// does.
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
// adding tok's own probability only when its own text is not already among
// them.
func rawMassPerClass(tok ports.Token, classes map[string][]string) map[string]float64 {
	mass := make(map[string]float64)
	ownTextSeen := false
	for _, alt := range tok.Alternatives {
		if class := classOf(alt.Text, classes); class != "" {
			mass[class] += math.Exp(alt.LogProb)
		}
		if alt.Text == tok.Text {
			ownTextSeen = true
		}
	}
	if !ownTextSeen {
		if class := classOf(tok.Text, classes); class != "" {
			mass[class] += math.Exp(tok.LogProb)
		}
	}
	return mass
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

package app

import (
	"slices"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Candidate is a policy rule a person might promote, and the subjects whose
// decisions suggest it.
type Candidate struct {
	Rule     PolicyRule `json:"rule"`
	Evidence []string   `json:"evidence"`
}

// suggestKey groups decisions by the verdict they followed and the policy
// decision their choice maps to.
type suggestKey struct {
	verdict  string
	decision string
}

// Suggest reads decision rows and, for each verdict, proposes a rule when
// at least minRepeats distinct subjects got a choice that choiceToDecision
// maps to allow or deny. A decision with no verdict, or whose choice maps
// to anything else, is ignored. The rule matches rows by that verdict; the
// evidence is the sorted subject ids. Candidates are sorted by rule id.
func Suggest(decisions []ports.Record, choiceToDecision map[string]string, minRepeats int) []Candidate {
	groups := map[suggestKey][]string{}
	for _, r := range decisions {
		verdict, decision := r.Fields["verdict_at_decision"], choiceToDecision[r.Fields["decision"]]
		if verdict == "" || (decision != DecisionAllow && decision != DecisionDeny) {
			continue
		}
		k := suggestKey{verdict, decision}
		groups[k] = append(groups[k], r.Fields["subject_id"])
	}

	var out []Candidate
	for k, subjects := range groups {
		slices.Sort(subjects)
		subjects = slices.Compact(subjects)
		if len(subjects) < minRepeats {
			continue
		}
		out = append(out, Candidate{Rule: verdictRule(k), Evidence: subjects})
	}
	slices.SortFunc(out, func(a, b Candidate) int { return strings.Compare(a.Rule.ID, b.Rule.ID) })
	return out
}

// verdictRule is the rule that gives k's decision to every row with k's
// verdict.
func verdictRule(k suggestKey) PolicyRule {
	return PolicyRule{
		ID:       k.decision + "-when-verdict-" + k.verdict,
		Decision: k.decision,
		When:     []Condition{{Field: "verdict", Op: "==", Value: k.verdict}},
	}
}

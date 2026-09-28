package app

import (
	"maps"
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
// at least minRepeats subjects' latest decisions carry a choice that
// choiceToDecision maps to allow or deny. Only each subject's latest
// decision counts. A latest decision with no verdict, or whose choice maps
// to anything else, is ignored. The rule matches rows by that verdict; the
// evidence is the sorted subject ids. Candidates are sorted by rule id.
func Suggest(decisions []ports.Record, choiceToDecision map[string]string, minRepeats int) []Candidate {
	groups := map[suggestKey][]string{}
	for _, r := range latestPerSubject(decisions) {
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
		if len(subjects) < minRepeats {
			continue
		}
		out = append(out, Candidate{Rule: verdictRule(k), Evidence: subjects})
	}
	slices.SortFunc(out, func(a, b Candidate) int { return strings.Compare(a.Rule.ID, b.Rule.ID) })
	return out
}

// latestPerSubject keeps each subject's decision with the latest time; of
// two at the same time, the later in decisions wins.
func latestPerSubject(decisions []ports.Record) []ports.Record {
	latest := map[string]ports.Record{}
	for _, r := range decisions {
		id := r.Fields["subject_id"]
		if prev, seen := latest[id]; !seen || !r.When.Before(prev.When) {
			latest[id] = r
		}
	}
	return slices.Collect(maps.Values(latest))
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

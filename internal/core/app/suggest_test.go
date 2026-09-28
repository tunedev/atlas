package app_test

import (
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func decisionRow(subject, choice, verdict string) ports.Record {
	return ports.Record{Kind: "decision", Fields: map[string]string{
		"subject_id": subject, "decision": choice, "verdict_at_decision": verdict,
	}}
}

var bakeChoices = map[string]string{"bake": "allow", "skip": "deny", "later": "ask"}

func TestSuggestPromotesAChoiceRepeatedEnoughTimes(t *testing.T) {
	rows := []ports.Record{
		decisionRow("order-3", "skip", "reach"),
		decisionRow("order-1", "skip", "reach"),
		decisionRow("order-2", "skip", "reach"),
		decisionRow("order-4", "bake", "take"),
		decisionRow("order-5", "skip", ""),
		decisionRow("order-6", "skip", ""),
		decisionRow("order-7", "skip", ""),
	}

	got := app.Suggest(rows, bakeChoices, 3)

	want := []app.Candidate{{
		Rule: app.PolicyRule{ID: "deny-when-verdict-reach", Decision: "deny",
			When: []app.Condition{{Field: "verdict", Op: "==", Value: "reach"}}},
		Evidence: []string{"order-1", "order-2", "order-3"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSuggestNamesNothingBelowTheThreshold(t *testing.T) {
	rows := []ports.Record{decisionRow("order-1", "skip", "reach"), decisionRow("order-2", "skip", "reach")}
	if got := app.Suggest(rows, bakeChoices, 3); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestSuggestIgnoresAChoiceWithNoAllowOrDeny(t *testing.T) {
	rows := []ports.Record{
		decisionRow("order-1", "later", "reach"), decisionRow("order-2", "later", "reach"), decisionRow("order-3", "later", "reach"),
		decisionRow("order-4", "frost", "reach"), decisionRow("order-5", "frost", "reach"), decisionRow("order-6", "frost", "reach"),
	}
	if got := app.Suggest(rows, bakeChoices, 3); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestSuggestCountsEachSubjectOnceAndSortsByRuleID(t *testing.T) {
	rows := []ports.Record{
		decisionRow("order-1", "skip", "reach"), decisionRow("order-1", "skip", "reach"), decisionRow("order-2", "skip", "reach"),
		decisionRow("order-4", "skip", "stale"), decisionRow("order-5", "skip", "stale"), decisionRow("order-3", "skip", "stale"),
		decisionRow("order-6", "bake", "take"), decisionRow("order-7", "bake", "take"), decisionRow("order-8", "bake", "take"),
	}
	got := app.Suggest(rows, bakeChoices, 3)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.Rule.ID)
	}
	if want := []string{"allow-when-verdict-take", "deny-when-verdict-stale"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

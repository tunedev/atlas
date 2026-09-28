package tools_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

const bakeChoicesYAML = "bake: allow\nskip: deny\n"

func recordBakeDecision(t *testing.T, docs ports.Docs, index ports.Index, subject, choice, verdict string) {
	t.Helper()
	_, err := app.RecordDecision(context.Background(), docs, index, app.Decision{
		SubjectID: subject, Choice: choice, VerdictAtDecision: verdict, When: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPolicySuggestProposesARuleAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	for _, s := range []string{"order-1", "order-2", "order-3"} {
		recordBakeDecision(t, docs, index, s, "skip", "reach")
	}
	recordBakeDecision(t, docs, index, "order-4", "bake", "take")
	pathsBefore, _ := docs.List(ctx, "")
	rowsBefore, _ := index.Find(ctx, ports.Query{Kind: "decision"})

	out, err := tools.NewPolicySuggest(index).Invoke(ctx, map[string]string{"choices": bakeChoicesYAML})
	if err != nil {
		t.Fatalf("policy.suggest: %v", err)
	}

	body, _ := json.Marshal(out)
	want := `{"candidates":[{"rule":{"id":"deny-when-verdict-reach","decision":"deny","when":[{"field":"verdict","op":"==","value":"reach"}]},"evidence":["order-1","order-2","order-3"]}]}`
	if string(body) != want {
		t.Errorf("out = %s", body)
	}
	pathsAfter, _ := docs.List(ctx, "")
	rowsAfter, _ := index.Find(ctx, ports.Query{Kind: "decision"})
	if !reflect.DeepEqual(pathsBefore, pathsAfter) || len(rowsBefore) != len(rowsAfter) {
		t.Errorf("store changed: %v -> %v, %d -> %d rows", pathsBefore, pathsAfter, len(rowsBefore), len(rowsAfter))
	}
}

func TestPolicySuggestHonoursMinRepeats(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	recordBakeDecision(t, docs, index, "order-1", "skip", "reach")
	recordBakeDecision(t, docs, index, "order-2", "skip", "reach")
	suggest := tools.NewPolicySuggest(index)

	byDefault, err := suggest.Invoke(ctx, map[string]string{"choices": bakeChoicesYAML})
	if err != nil || len(byDefault.(map[string]any)["candidates"].([]app.Candidate)) != 0 {
		t.Errorf("default = %v, %v", byDefault, err)
	}
	atTwo, err := suggest.Invoke(ctx, map[string]string{"choices": bakeChoicesYAML, "min_repeats": "2"})
	if err != nil || len(atTwo.(map[string]any)["candidates"].([]app.Candidate)) != 1 {
		t.Errorf("at two = %v, %v", atTwo, err)
	}
}

func TestPolicySuggestRefusesBadInputs(t *testing.T) {
	_, index := store(t)
	for name, with := range map[string]map[string]string{
		"min_repeats of one":  {"choices": bakeChoicesYAML, "min_repeats": "1"},
		"min_repeats not int": {"choices": bakeChoicesYAML, "min_repeats": "many"},
		"no choices":          {},
		"choices not a map":   {"choices": "- bake\n"},
	} {
		_, err := tools.NewPolicySuggest(index).Invoke(context.Background(), with)
		if err == nil || !strings.HasPrefix(err.Error(), "policy.suggest: ") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

const staleRule = `{"id": "deny-when-verdict-stale", "decision": "deny", "when": [{"field": "verdict", "op": "==", "value": "stale"}]}`
const takeRule = `{"id": "allow-when-verdict-take", "decision": "allow", "when": [{"field": "verdict", "op": "==", "value": "take"}]}`

func TestPolicyAddCreatesThenAppends(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	add := tools.NewPolicyAdd(docs, index)

	if _, err := add.Invoke(ctx, map[string]string{"policy": "profile/policy.json", "rule": staleRule, "evidence": "order-1, order-2, order-3"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := add.Invoke(ctx, map[string]string{"policy": "profile/policy.json", "rule": takeRule, "evidence": "order-6, order-7, order-8"}); err != nil {
		t.Fatalf("second add: %v", err)
	}

	body, err := docs.Get(ctx, "profile/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := app.ParsePolicy(body)
	if err != nil || len(rules) != 2 || rules[0].ID != "deny-when-verdict-stale" || rules[1].ID != "allow-when-verdict-take" {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	history, err := docs.History(ctx, "profile/policy.json")
	if err != nil || len(history) != 2 || history[0].Message != "Promote policy rule allow-when-verdict-take: order-6, order-7, order-8" {
		t.Errorf("history = %+v, %v", history, err)
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "profile", Match: map[string]string{"section": "policy"}})
	if err != nil || len(rows) != 1 || rows[0].Path != "profile/policy.json" {
		t.Errorf("rows = %v, %v", rows, err)
	}
}

func TestPolicyAddRefusesADuplicateOrMalformedRule(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	add := tools.NewPolicyAdd(docs, index)
	if _, err := add.Invoke(ctx, map[string]string{"policy": "profile/policy.json", "rule": staleRule, "evidence": "order-1"}); err != nil {
		t.Fatal(err)
	}

	for name, rule := range map[string]string{
		"duplicate id":     staleRule,
		"ask decision":     `{"id": "later", "decision": "ask", "when": [{"field": "verdict", "op": "==", "value": "take"}]}`,
		"no conditions":    `{"id": "bare", "decision": "allow", "when": []}`,
		"not json":         `{"id": `,
		"unknown operator": `{"id": "odd", "decision": "allow", "when": [{"field": "verdict", "op": "~", "value": "take"}]}`,
	} {
		_, err := add.Invoke(ctx, map[string]string{"policy": "profile/policy.json", "rule": rule, "evidence": "order-2"})
		if err == nil || !strings.HasPrefix(err.Error(), "policy.add: ") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	history, _ := docs.History(ctx, "profile/policy.json")
	if len(history) != 1 {
		t.Errorf("a refused rule was committed: %d revisions", len(history))
	}
}

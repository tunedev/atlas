package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

const bakeryPolicy = `{"rules": [
  {"id": "big-order", "decision": "allow",
   "when": [{"field": "verdict", "op": "==", "value": "bake"}, {"field": "p", "op": ">=", "value": 0.7}]},
  {"id": "no-nuts", "decision": "deny",
   "when": [{"field": "answers.allergen", "op": "==", "value": "nuts"}]}
]}`

func bakeryRow(id, verdict string, p float64, allergen string) map[string]any {
	return map[string]any{"subject_id": id, "verdict": verdict, "p": p,
		"answers": map[string]any{"allergen": allergen}, "rules": []any{}}
}

func bakeryRows(rows ...map[string]any) string {
	body, _ := json.Marshal(rows)
	return string(body)
}

func TestPolicyDecideAddsADecisionToEachRow(t *testing.T) {
	docs, _ := store(t)
	if _, err := docs.Put(context.Background(), "profile/policy.json", []byte(bakeryPolicy), "policy"); err != nil {
		t.Fatalf("put policy: %v", err)
	}

	rows := bakeryRows(
		bakeryRow("allow-1", "bake", 0.9, "none"),
		bakeryRow("deny-1", "bake", 0.9, "nuts"),
		map[string]any{"subject_id": "error-1", "error": "boom"},
	)
	out, err := tools.NewPolicyDecide(docs).Invoke(context.Background(), map[string]string{
		"rows": rows, "policy": "profile/policy.json",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	got := out.(map[string]any)["rows"].([]any)
	if len(got) != 3 {
		t.Fatalf("rows = %v", got)
	}
	allow := got[0].(map[string]any)
	if allow["decision"] != "allow" {
		t.Errorf("allow row = %v", allow)
	}
	deny := got[1].(map[string]any)
	if deny["decision"] != "deny" {
		t.Errorf("deny row = %v", deny)
	}
	errRow := got[2].(map[string]any)
	if _, present := errRow["decision"]; present {
		t.Errorf("error row gained a decision: %v", errRow)
	}
	if errRow["error"] != "boom" {
		t.Errorf("error row changed: %v", errRow)
	}
}

func TestPolicyDecideWithNoPolicyAsksUnlessTripped(t *testing.T) {
	docs, _ := store(t)
	tripped := bakeryRow("tripped-1", "bake", 0.9, "none")
	tripped["rules"] = []any{map[string]any{"id": "oven-broken", "state": "tripped"}}
	rows := bakeryRows(bakeryRow("plain-1", "bake", 0.9, "none"), tripped)

	out, err := tools.NewPolicyDecide(docs).Invoke(context.Background(), map[string]string{
		"rows": rows, "policy": "",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got := out.(map[string]any)["rows"].([]any)
	if got[0].(map[string]any)["decision"] != "ask" {
		t.Errorf("plain row = %v, want ask with no policy", got[0])
	}
	if got[1].(map[string]any)["decision"] != "deny" {
		t.Errorf("tripped row = %v, want deny even with no policy", got[1])
	}
}

func TestPolicyDecideRejectsAMalformedPolicyNamingThePath(t *testing.T) {
	docs, _ := store(t)
	if _, err := docs.Put(context.Background(), "profile/broken-policy.json", []byte(`{`), "policy"); err != nil {
		t.Fatalf("put policy: %v", err)
	}
	_, err := tools.NewPolicyDecide(docs).Invoke(context.Background(), map[string]string{
		"rows": bakeryRows(bakeryRow("a", "bake", 0.9, "none")), "policy": "profile/broken-policy.json",
	})
	if err == nil {
		t.Fatal("a malformed policy was accepted")
	}
	if !strings.Contains(err.Error(), "profile/broken-policy.json") {
		t.Errorf("error does not name the path: %v", err)
	}
}

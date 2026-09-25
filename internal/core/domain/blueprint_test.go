package domain_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

func aBlueprint() domain.Blueprint {
	return domain.Blueprint{Name: "tides", Vars: map[string]string{"port": "Dover", "day": "monday"}}
}

func TestWithVarsReplacesADeclaredVar(t *testing.T) {
	b, err := aBlueprint().WithVars(map[string]string{"port": "Calais"})
	if err != nil {
		t.Fatalf("WithVars: %v", err)
	}
	if b.Vars["port"] != "Calais" || b.Vars["day"] != "monday" {
		t.Errorf("vars = %v, want port overridden and day kept", b.Vars)
	}
}

func TestWithVarsLeavesTheOriginalUntouched(t *testing.T) {
	orig := aBlueprint()
	if _, err := orig.WithVars(map[string]string{"port": "Calais"}); err != nil {
		t.Fatalf("WithVars: %v", err)
	}
	if orig.Vars["port"] != "Dover" {
		t.Errorf("the original blueprint's vars were mutated: %v", orig.Vars)
	}
}

func TestWithVarsRejectsAVarThePackDoesNotDeclare(t *testing.T) {
	_, err := aBlueprint().WithVars(map[string]string{"prot": "Calais"})
	if err == nil {
		t.Fatal("a misspelt var was accepted and would be silently ignored")
	}
	if !strings.Contains(err.Error(), "prot") {
		t.Errorf("error does not name the var: %v", err)
	}
}

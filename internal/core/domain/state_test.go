package domain_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

func TestStateKeepsVarsAndStepOutputsApart(t *testing.T) {
	s := domain.NewState(map[string]string{"board": "acme"})
	s.Put("first", map[string]any{"title": "a title"})

	if got := s.Vars()["board"]; got != "acme" {
		t.Errorf("Vars()[board] = %q", got)
	}
	out, ok := s.Outputs()["first"]
	if !ok {
		t.Fatal("Outputs() has no entry for step first")
	}
	m, ok := out.(map[string]any)
	if !ok || m["title"] != "a title" {
		t.Errorf("Outputs()[first] = %#v", out)
	}
}

func TestOutputsIsNotNilBeforeAnyStepRuns(t *testing.T) {
	// Outputs and Vars must never panic on a nil map, even before any step
	// has run. A template referencing a step that has not run still fails at
	// render time, via missingkey=error, rather than rendering empty.
	if domain.NewState(nil).Outputs() == nil {
		t.Error("Outputs() is nil on a fresh State")
	}
	if domain.NewState(nil).Vars() == nil {
		t.Error("Vars() is nil on a fresh State")
	}
}

func TestPutOverwritesTheSameStepID(t *testing.T) {
	s := domain.NewState(nil)
	s.Put("x", "first")
	s.Put("x", "second")
	if s.Outputs()["x"] != "second" {
		t.Errorf("Outputs()[x] = %v, want second", s.Outputs()["x"])
	}
}

func TestAccessorsReturnCopiesSoMutationDoesNotAffectState(t *testing.T) {
	s := domain.NewState(map[string]string{"key": "value"})
	s.Put("step", "output")

	// Mutate the returned vars map.
	vars := s.Vars()
	vars["key"] = "tampered"
	vars["new"] = "added"

	// Mutate the returned outputs map.
	outputs := s.Outputs()
	outputs["step"] = "forged"
	outputs["new"] = "forged output"

	// State should be unchanged.
	if got := s.Vars()["key"]; got != "value" {
		t.Errorf("Vars()[key] = %q, want value", got)
	}
	if _, ok := s.Vars()["new"]; ok {
		t.Error("Vars() has new key, should not")
	}
	if got := s.Outputs()["step"]; got != "output" {
		t.Errorf("Outputs()[step] = %q, want output", got)
	}
	if _, ok := s.Outputs()["new"]; ok {
		t.Error("Outputs() has new key, should not")
	}
}

package packfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadParsesAPack(t *testing.T) {
	path := write(t, `
name: example
vars:
  host: example.com
steps:
  - id: one
    tool: http.request
    with:
      url: "https://{{ .vars.host }}/a"
      select: items.0
  - id: two
    tool: model.complete
    with:
      system: be terse
      user: "{{ .steps.one.title }}"
`)

	b, err := packfile.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Name != "example" {
		t.Errorf("Name = %q", b.Name)
	}
	if b.Vars["host"] != "example.com" {
		t.Errorf("Vars = %#v", b.Vars)
	}
	if len(b.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(b.Steps))
	}
	if b.Steps[0].ID != "one" || b.Steps[0].Tool != "http.request" {
		t.Errorf("step 0 = %#v", b.Steps[0])
	}
	if b.Steps[0].With["select"] != "items.0" {
		t.Errorf("step 0 with = %#v", b.Steps[0].With)
	}
	if b.Steps[1].With["system"] != "be terse" {
		t.Errorf("step 1 with = %#v", b.Steps[1].With)
	}
}

func TestLoadRejectsAStepWithNoID(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - tool: http.request\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a step with no id; later steps reference output by id")
	}
}

func TestLoadRejectsADuplicateStepID(t *testing.T) {
	path := write(t, `
name: x
steps:
  - id: same
    tool: a
  - id: same
    tool: b
`)
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a duplicate step id; the second would overwrite the first's output")
	}
}

func TestLoadRejectsAStepWithNoTool(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - id: one\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a step with no tool")
	}
}

func TestLoadRejectsAPackWithNoSteps(t *testing.T) {
	path := write(t, "name: x\nsteps: []\n")
	if _, err := packfile.Load(path); err == nil {
		t.Error("Load accepted a pack with no steps")
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	if _, err := packfile.Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("Load succeeded on a missing file")
	}
}

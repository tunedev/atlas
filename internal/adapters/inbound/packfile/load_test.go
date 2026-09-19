package packfile_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	b, err := packfile.Load(path)
	if err == nil {
		t.Error("Load accepted a step with no id; later steps reference output by id")
	}
	if b.Name != "" || len(b.Steps) != 0 {
		t.Errorf("returned non-zero Blueprint on error: Name=%q, Steps=%d", b.Name, len(b.Steps))
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
	b, err := packfile.Load(path)
	if err == nil {
		t.Error("Load accepted a duplicate step id; the second would overwrite the first's output")
	}
	if b.Name != "" || len(b.Steps) != 0 {
		t.Errorf("returned non-zero Blueprint on error: Name=%q, Steps=%d", b.Name, len(b.Steps))
	}
}

func TestLoadRejectsAStepWithNoTool(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - id: one\n")
	b, err := packfile.Load(path)
	if err == nil {
		t.Error("Load accepted a step with no tool")
	}
	if b.Name != "" || len(b.Steps) != 0 {
		t.Errorf("returned non-zero Blueprint on error: Name=%q, Steps=%d", b.Name, len(b.Steps))
	}
}

func TestLoadRejectsAPackWithNoSteps(t *testing.T) {
	path := write(t, "name: x\nsteps: []\n")
	b, err := packfile.Load(path)
	if err == nil {
		t.Error("Load accepted a pack with no steps")
	}
	if b.Name != "" || len(b.Steps) != 0 {
		t.Errorf("returned non-zero Blueprint on error: Name=%q, Steps=%d", b.Name, len(b.Steps))
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	if _, err := packfile.Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("Load succeeded on a missing file")
	}
}

// A step id becomes a Go template map key via .steps.<id>, so it must be a
// legal template identifier. Anything else either fails at render time with
// an error naming neither the pack file nor the id, or, worse, parses and
// silently resolves to the wrong path.
func TestLoadRejectsStepIDsThatAreNotValidTemplateIdentifiers(t *testing.T) {
	cases := []string{"fetch-top", "fetch.item", " ", "1leading"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			path := write(t, fmt.Sprintf("name: x\nsteps:\n  - id: %q\n    tool: a\n", id))
			b, err := packfile.Load(path)
			if err == nil {
				t.Errorf("Load accepted step id %q", id)
			}
			if b.Name != "" || len(b.Steps) != 0 {
				t.Errorf("returned non-zero Blueprint on error: Name=%q, Steps=%d", b.Name, len(b.Steps))
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the pack file", err)
			}
			if !strings.Contains(err.Error(), id) {
				t.Errorf("error %q does not name the offending id %q", err, id)
			}
		})
	}
}

func TestLoadAcceptsAStepIDThatIsAValidTemplateIdentifier(t *testing.T) {
	path := write(t, "name: x\nsteps:\n  - id: fetch_top\n    tool: a\n")
	if _, err := packfile.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

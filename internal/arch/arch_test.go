package arch_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The core owns its interfaces and must not import any driver or adapter.
// This is the mechanical form of "dependencies point inward".
func TestCoreImportsNoAdapters(t *testing.T) {
	// Module-qualified, not dot-relative: go test runs this binary with cwd
	// set to this package's directory, never the module root.
	out, err := exec.Command("go", "list", "-deps", "github.com/tunedev/atlas/internal/core/...").Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}
	// Adapter tree and third-party drivers only. Never list stdlib packages:
	// go list -deps is transitive, so net/http would fail this for the wrong reason.
	forbidden := []string{
		"github.com/tunedev/atlas/internal/adapters",
		"go.opentelemetry.io/otel/exporters",
		"gopkg.in/yaml.v3",
	}
	for _, dep := range strings.Split(string(out), "\n") {
		for _, bad := range forbidden {
			if strings.HasPrefix(strings.TrimSpace(dep), bad) {
				t.Errorf("core imports %q; dependencies must point inward", dep)
			}
		}
	}
}

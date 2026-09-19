package arch_test

import (
	"os/exec"
	"strings"
	"testing"
)

// runGoListDeps runs `go list -deps` on pkg and fails the test with the
// command's stderr on error, rather than the bare exit status Output()
// alone would report.
func runGoListDeps(t *testing.T, pkg string) []byte {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v: %s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	return out
}

// The core owns its interfaces and must not import any driver or adapter.
// This is the mechanical form of "dependencies point inward".
func TestCoreImportsNoAdapters(t *testing.T) {
	// Module-qualified, not dot-relative: go test runs this binary with cwd
	// set to this package's directory, never the module root.
	out := runGoListDeps(t, "github.com/tunedev/atlas/internal/core/...")
	// Adapter tree and third-party drivers only. Never list stdlib packages:
	// go list -deps is transitive, so net/http would fail this for the wrong reason.
	//
	// The otel root package is forbidden by exact name, not prefix: it pulls in
	// propagation, which pulls in net/http and the TLS stack, but its
	// subpackages (trace, attribute, codes) are plain types with no such
	// weight and share its string prefix, so a prefix match here would also
	// reject those.
	forbiddenExact := []string{
		"go.opentelemetry.io/otel",
	}
	forbiddenPrefix := []string{
		"github.com/tunedev/atlas/internal/adapters",
		"go.opentelemetry.io/otel/exporters",
		"go.opentelemetry.io/otel/propagation",
		"gopkg.in/yaml.v3",
	}
	for _, dep := range strings.Split(string(out), "\n") {
		dep = strings.TrimSpace(dep)
		for _, bad := range forbiddenExact {
			if dep == bad {
				t.Errorf("core imports %q; dependencies must point inward", dep)
			}
		}
		for _, bad := range forbiddenPrefix {
			if strings.HasPrefix(dep, bad) {
				t.Errorf("core imports %q; dependencies must point inward", dep)
			}
		}
	}
}

// The core must never compile in a network or TLS stack. This is the
// property the otel root-package ban exists to protect, checked directly so
// a future import of anything that drags in net/http or crypto/tls fails
// here even if it does not go through otel.
func TestCoreHasNoNetworkOrTLSDependency(t *testing.T) {
	out := runGoListDeps(t, "github.com/tunedev/atlas/internal/core/...")
	forbidden := map[string]bool{"net/http": true, "crypto/tls": true}
	for _, dep := range strings.Split(string(out), "\n") {
		dep = strings.TrimSpace(dep)
		if forbidden[dep] {
			t.Errorf("core imports %q; the core must not compile in a network or TLS stack", dep)
		}
	}
}

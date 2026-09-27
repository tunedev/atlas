package arch_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	inboundRoot   = "github.com/tunedev/atlas/internal/adapters/inbound/"
	outboundRoot  = "github.com/tunedev/atlas/internal/adapters/outbound/"
	configRoot    = "github.com/tunedev/atlas/internal/config"
	telemetryRoot = "github.com/tunedev/atlas/internal/telemetry"
)

// TestInboundAdaptersStayIsolated holds the seam every surface plugs into:
// an inbound adapter reaches the core, the standard library and third-party
// packages, never another inbound adapter, never an outbound one, and never
// internal/config or internal/telemetry. Those two, like outbound adapters,
// are wired once, at the composition root, so a surface that imported one
// would stand up its own copy of something every surface shares. Every
// package under inbound/ is checked, including ones added after this test
// was written.
func TestInboundAdaptersStayIsolated(t *testing.T) {
	pkgs := inboundPackages(t)
	for _, want := range []string{inboundRoot + "packfile", inboundRoot + "mcpserve"} {
		if !slices.Contains(pkgs, want) {
			t.Fatalf("inbound packages %v do not include %s; the enumeration is broken, not the seam", pkgs, want)
		}
	}
	for _, pkg := range pkgs {
		deps := strings.Fields(string(runGoListDeps(t, pkg)))
		for _, dep := range isolationViolations(pkg, deps) {
			t.Errorf("%s depends on %s; an inbound adapter may reach the core, never another surface, an outbound adapter, or composition-root infrastructure (config, telemetry)", pkg, dep)
		}
	}
}

// TestIsolationRuleSeparatesSurfaces checks the rule itself against
// synthetic dependency lists, so a future surface's likely mistakes are
// pinned before anyone makes them.
func TestIsolationRuleSeparatesSurfaces(t *testing.T) {
	web := inboundRoot + "web"
	cases := []struct {
		dep       string
		violation bool
	}{
		{inboundRoot + "packfile", true},         // a sibling surface
		{inboundRoot + "webhook", true},          // a sibling sharing a name prefix
		{outboundRoot + "gitdocs", true},         // an outbound adapter
		{outboundRoot + "openaiprov/wire", true}, // an outbound subpackage
		{configRoot, true},                       // composition-root config, forbidden
		{telemetryRoot, true},                    // composition-root telemetry, forbidden
		{configRoot + "x", false},                // a name-prefix neighbour, allowed
		{web, false},                             // itself
		{web + "/tmpl", false},                   // its own subpackage
		{"github.com/tunedev/atlas/internal/core/app", false},
		{"github.com/tunedev/atlas/internal/core/ports", false},
		{"net/http", false},
	}
	for _, c := range cases {
		got := isolationViolations(web, []string{c.dep})
		if (len(got) == 1) != c.violation {
			t.Errorf("dependency %s: violations %v, want violation=%v", c.dep, got, c.violation)
		}
	}
}

// TestEveryInternalPackageHasAKnownHome makes sure a surface built outside
// internal/adapters/inbound/ cannot hide from TestInboundAdaptersStayIsolated:
// every package under internal/ must sit in one of the tree's known homes.
func TestEveryInternalPackageHasAKnownHome(t *testing.T) {
	knownHomes := []string{
		"github.com/tunedev/atlas/internal/core",
		inboundRoot,
		outboundRoot,
		"github.com/tunedev/atlas/internal/arch",
		configRoot,
		telemetryRoot,
		"github.com/tunedev/atlas/internal/pidlock",
	}
	out, err := exec.Command("go", "list", "github.com/tunedev/atlas/internal/...").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v: %s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !hasKnownHome(pkg, knownHomes) {
			t.Errorf("%s has no known home; a new surface belongs under %s, and anything else needs a deliberate home added to this list", pkg, inboundRoot)
		}
	}
}

// isolationViolations returns the dependencies of pkg that break the seam:
// any inbound package that is not pkg or below it, any outbound package, and
// internal/config or internal/telemetry (or anything below them) — the
// composition-root infrastructure cmd/atlas wires exactly once.
func isolationViolations(pkg string, deps []string) []string {
	var bad []string
	for _, dep := range deps {
		own := dep == pkg || strings.HasPrefix(dep, pkg+"/")
		switch {
		case own:
		case strings.HasPrefix(dep, inboundRoot), strings.HasPrefix(dep, outboundRoot):
			bad = append(bad, dep)
		case isExactOrBelow(dep, configRoot), isExactOrBelow(dep, telemetryRoot):
			bad = append(bad, dep)
		}
	}
	return bad
}

// isExactOrBelow reports whether dep is root itself or a package below it.
func isExactOrBelow(dep, root string) bool {
	return dep == root || strings.HasPrefix(dep, root+"/")
}

// hasKnownHome reports whether pkg is one of homes or sits below one.
func hasKnownHome(pkg string, homes []string) bool {
	for _, home := range homes {
		home = strings.TrimSuffix(home, "/")
		if isExactOrBelow(pkg, home) {
			return true
		}
	}
	return false
}

// inboundPackages lists every package under internal/adapters/inbound.
func inboundPackages(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", inboundRoot+"...").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v: %s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	return strings.Fields(string(out))
}

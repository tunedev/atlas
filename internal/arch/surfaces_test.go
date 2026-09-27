package arch_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	inboundRoot  = "github.com/tunedev/atlas/internal/adapters/inbound/"
	outboundRoot = "github.com/tunedev/atlas/internal/adapters/outbound/"
)

// TestInboundAdaptersStayIsolated holds the seam every surface plugs into:
// an inbound adapter reaches the core, the standard library and third-party
// packages, never another inbound adapter and never an outbound one.
// Outbound adapters are wired once, at the composition root, so a surface
// that imported one would stand up its own copy of something every surface
// shares. Every package under inbound/ is checked, including ones added
// after this test was written.
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
			t.Errorf("%s depends on %s; an inbound adapter may reach the core, never another surface or an outbound adapter", pkg, dep)
		}
	}
}

// TestIsolationRuleSeparatesSurfaces checks the rule itself against
// dependency lists no real package has yet, so a future surface's likely
// mistakes are pinned before anyone makes them.
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

// isolationViolations returns the dependencies of pkg that break the seam:
// any inbound package that is not pkg or below it, and any outbound package.
func isolationViolations(pkg string, deps []string) []string {
	var bad []string
	for _, dep := range deps {
		own := dep == pkg || strings.HasPrefix(dep, pkg+"/")
		switch {
		case own:
		case strings.HasPrefix(dep, inboundRoot), strings.HasPrefix(dep, outboundRoot):
			bad = append(bad, dep)
		}
	}
	return bad
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

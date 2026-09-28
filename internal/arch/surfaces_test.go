package arch_test

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	modulePath    = "github.com/tunedev/atlas"
	coreRoot      = modulePath + "/internal/core/"
	inboundRoot   = modulePath + "/internal/adapters/inbound/"
	outboundRoot  = modulePath + "/internal/adapters/outbound/"
	configRoot    = modulePath + "/internal/config"
	telemetryRoot = modulePath + "/internal/telemetry"
)

// subtreeHomes hold any package below them; exactHomes are single packages
// with nothing allowed below.
var (
	subtreeHomes = []string{coreRoot, inboundRoot, outboundRoot}
	exactHomes   = []string{
		modulePath + "/internal/arch",
		configRoot,
		telemetryRoot,
		modulePath + "/internal/pidlock",
		modulePath + "/cmd/atlas",
	}
)

// TestInboundAdaptersStayIsolated holds the seam every surface plugs into:
// inside the module, an inbound adapter reaches only the core and its own
// subtree. Everything else in the module, present or future, is wired once
// at the composition root, so a surface that imported it would stand up its
// own copy of something every surface shares. The standard library and
// third-party packages are allowed. Every package under inbound/ is checked,
// including ones added after this test was written.
func TestInboundAdaptersStayIsolated(t *testing.T) {
	witnessModuleTree(t)
	pkgs := inboundPackages(t)
	for _, want := range []string{inboundRoot + "packfile", inboundRoot + "mcpserve"} {
		if !slices.Contains(pkgs, want) {
			t.Fatalf("inbound packages %v do not include %s; the enumeration is broken, not the seam", pkgs, want)
		}
	}
	for _, pkg := range pkgs {
		deps := strings.Fields(string(runGoListDeps(t, pkg)))
		for _, dep := range isolationViolations(pkg, deps) {
			t.Errorf("%s depends on %s; inside the module an inbound adapter may reach only %s and its own subtree", pkg, dep, coreRoot)
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
		{modulePath + "/internal/pidlock", true}, // composition-root locking, forbidden
		{modulePath + "/internal/shared", true},  // an unknown future module package, forbidden
		{configRoot + "x", true},                 // a name-prefix neighbour, forbidden like any module package
		{modulePath + "/internal/corex", true},   // a name-prefix neighbour of the core
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

// TestEveryPackageHasAKnownHome makes sure a surface built outside
// internal/adapters/inbound/ cannot hide from TestInboundAdaptersStayIsolated:
// every package in the module must sit in one of its known homes.
func TestEveryPackageHasAKnownHome(t *testing.T) {
	witnessModuleTree(t)
	out, err := exec.Command("go", "list", modulePath+"/...").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v: %s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !hasKnownHome(pkg) {
			t.Errorf("%s has no known home; a new surface belongs under %s, and anything else needs a deliberate home added to this test", pkg, inboundRoot)
		}
	}
}

// TestKnownHomesAreExactWhereTheyShouldBe pins the home rule against
// synthetic package paths.
func TestKnownHomesAreExactWhereTheyShouldBe(t *testing.T) {
	cases := []struct {
		pkg  string
		home bool
	}{
		{coreRoot + "app", true},
		{coreRoot + "app/sub", true},
		{inboundRoot + "web", true},
		{inboundRoot + "web/tmpl", true},
		{outboundRoot + "gitdocs", true},
		{modulePath + "/internal/pidlock", true},
		{modulePath + "/cmd/atlas", true},
		{modulePath + "/internal/pidlock/web", false},
		{configRoot + "/web", false},
		{modulePath + "/internal/web", false},
		{modulePath + "/web", false},
		{modulePath + "/cmd/atlasweb", false},
		{modulePath + "/cmd/atlas/web", false},
	}
	for _, c := range cases {
		if got := hasKnownHome(c.pkg); got != c.home {
			t.Errorf("hasKnownHome(%s) = %v, want %v", c.pkg, got, c.home)
		}
	}
}

// isolationViolations returns the dependencies of pkg that break the seam:
// any package in the module that is neither in the core nor pkg or below it.
func isolationViolations(pkg string, deps []string) []string {
	var bad []string
	for _, dep := range deps {
		inModule := isExactOrBelow(dep, modulePath)
		allowed := strings.HasPrefix(dep, coreRoot) || isExactOrBelow(dep, pkg)
		if inModule && !allowed {
			bad = append(bad, dep)
		}
	}
	return bad
}

// isExactOrBelow reports whether dep is root itself or a package below it.
func isExactOrBelow(dep, root string) bool {
	return dep == root || strings.HasPrefix(dep, root+"/")
}

// hasKnownHome reports whether pkg is a known home or sits in a subtree
// home.
func hasKnownHome(pkg string) bool {
	if slices.Contains(exactHomes, pkg) {
		return true
	}
	for _, root := range subtreeHomes {
		if strings.HasPrefix(pkg, root) {
			return true
		}
	}
	return false
}

// witnessModuleTree walks the module so Go's testlog records the walk: a
// package added anywhere in the module then invalidates the cached result of
// any test that calls this, instead of returning a stale PASS for a
// go-list-driven check the test cache cannot otherwise see.
func witnessModuleTree(t *testing.T) {
	t.Helper()
	walk := func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && (d.Name() == ".git" || d.Name() == ".superpowers") {
			return fs.SkipDir
		}
		return err
	}
	if err := filepath.WalkDir("../..", walk); err != nil {
		t.Fatalf("witness walk failed: %v", err)
	}
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

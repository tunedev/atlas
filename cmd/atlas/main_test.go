package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/config"
)

func TestBuildRegistryRegistersBothShippedTools(t *testing.T) {
	cfg := config.Config{}
	cfg.Pack.HTTPTimeout = 1234 * time.Millisecond
	cfg.Pack.HTTPMaxBytes = 4321
	cfg.Model.BaseURL = "http://example.invalid/v1"
	cfg.Model.Name = "a-model"
	cfg.Model.Timeout = 5678 * time.Millisecond
	cfg.Model.MaxBytes = 8765

	reg := buildRegistry(cfg)

	if _, ok := reg.Lookup("http.request"); !ok {
		t.Error("http.request is not registered")
	}
	if _, ok := reg.Lookup("model.complete"); !ok {
		t.Error("model.complete is not registered")
	}
	if _, ok := reg.Lookup("nonexistent.tool"); ok {
		t.Error("Lookup reported a tool that was never registered")
	}
}

// A hardcoded timeout and a configured one behave identically against a fast
// endpoint, so the property is checked structurally: buildRegistry must pass
// configured values through and never a literal of its own.
func TestBuildRegistryPassesNoLiterals(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "buildRegistry" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok {
				return true
			}
			if lit.Kind == token.INT || lit.Kind == token.FLOAT || lit.Kind == token.STRING {
				t.Errorf("buildRegistry contains the literal %s; every value must come from config", lit.Value)
			}
			return true
		})
		return
	}
	t.Fatal("buildRegistry not found in main.go")
}

// main() calls os.Exit; run() must therefore own every defer, or the
// telemetry shutdown never executes and spans are lost on the error path.
func TestRunIsSeparateFromMainSoDefersExecute(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	// Verify main() contains no defer statements.
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if _, ok := n.(*ast.DeferStmt); ok {
				t.Error("main() contains a defer; os.Exit will skip it")
			}
			return true
		})
	}

	// Verify os.Exit is called only from main(), not from run() or other functions.
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name == "main" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" || sel.Sel.Name != "Exit" {
				return true
			}
			t.Errorf("os.Exit called from %s(); defers will be skipped", fn.Name.Name)
			return true
		})
	}
}

// The runner defaults to a no-op tracer and no longer reads the global
// registry, so a composition root that does not call WithTracer produces no
// blueprint spans at all.
func TestCompositionRootInjectsATracer(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !bytes.Contains(src, []byte("WithTracer(")) {
		t.Error("main.go never calls WithTracer; the runner will keep its no-op tracer")
	}
	initAt := bytes.Index(src, []byte("telemetry.Init("))
	tracerAt := bytes.Index(src, []byte("otel.Tracer("))
	if initAt < 0 {
		t.Fatal("main.go never calls telemetry.Init")
	}
	if tracerAt < 0 {
		t.Fatal("main.go never obtains a tracer")
	}
	if tracerAt < initAt {
		t.Error("the tracer is obtained before telemetry.Init installs a provider; it will be the no-op one")
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/feedsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// testStore opens a gitdocs store and a sqlindex index over fresh temp
// directories, closing the index on cleanup.
func testStore(t *testing.T) (ports.Docs, ports.Index) {
	t.Helper()
	ctx := context.Background()
	docs, err := gitdocs.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("open docs: %v", err)
	}
	index, err := sqlindex.Open(ctx, t.TempDir()+"/index.db")
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return docs, index
}

// testCrawler builds a Crawler over a minimal valid config, standing in for
// the one buildRegistry receives from run().
func testCrawler(t *testing.T) *crawlsource.Crawler {
	t.Helper()
	c, err := crawlsource.NewCrawler(crawlsource.Config{
		UserAgent:     "atlas-test/1 (+https://example.invalid/bot)",
		Delay:         time.Millisecond,
		Timeout:       time.Second,
		PullTimeout:   time.Second,
		MaxBytes:      1 << 20,
		RenderTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new crawler: %v", err)
	}
	return c
}

func TestBuildRegistryRegistersBothShippedTools(t *testing.T) {
	cfg := config.Config{}
	cfg.Pack.HTTPTimeout = 1234 * time.Millisecond
	cfg.Pack.HTTPMaxBytes = 4321
	cfg.Model.BaseURL = "http://example.invalid/v1"
	cfg.Model.Name = "a-model"
	cfg.Model.Timeout = 5678 * time.Millisecond
	cfg.Model.MaxBytes = 8765
	cfg.Judge.TopLogProbs = 5
	cfg.Judge.MaxTokens = 256

	docs, index := testStore(t)
	reg := buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))

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

func TestBuildRegistryRegistersTheJudgeTool(t *testing.T) {
	docs, index := testStore(t)
	r := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	for _, name := range []string{"http.request", "model.complete", "judge.ask", "source.pull", "file.read", "file.text", "docs.put", "index.find", "docs.get", "quote.ground", "extract.run", "decision.record", "judge.each", "items.dedupe", "crawl.pull", "judge.outcome", "judge.calibrate", "decision.agreement"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}

func TestBuildRegistryRegistersTheTailoringTools(t *testing.T) {
	docs, index := testStore(t)
	r := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	for _, name := range []string{"text.spans", "span.resolve", "citations.judge", "claims.settle", "items.cite", "items.gather", "text.lines"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}

func TestRenderConfiguredButAbsentFailsAtStartup(t *testing.T) {
	cfg := config.Config{}
	cfg.Render.TypstPath = filepath.Join(t.TempDir(), "no-such-typst")
	cfg.Render.Timeout = time.Second
	cfg.Render.MaxBytes = 1 << 20
	_, err := startRender(cfg)
	if err == nil || !strings.Contains(err.Error(), "no-such-typst") {
		t.Errorf("err = %v", err)
	}
}

// A hardcoded timeout and a configured one behave identically against a fast
// endpoint, so the property is checked structurally: buildRegistry and
// startAgent must pass configured values through and never a literal of
// their own.
func TestCompositionPassesNoLiterals(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, name := range []string{"buildRegistry", "startAgent", "startRender"} {
		found := false
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			found = true
			ast.Inspect(fn, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok {
					t.Errorf("%s contains the literal %s; every value must come from config", name, lit.Value)
				}
				return true
			})
		}
		if !found {
			t.Errorf("%s not found in main.go", name)
		}
	}
}

// TestStartAgentFailsLoudlyForAMissingCommand proves startAgent surfaces the
// agent process's own failure to start, rather than hanging or returning a
// generic error.
func TestStartAgentFailsLoudlyForAMissingCommand(t *testing.T) {
	docs, index := testStore(t)
	cfg := config.Config{}
	cfg.Agent.Command = filepath.Join(t.TempDir(), "no-such-agent")
	cfg.Agent.Tools = []string{"http.request"}
	cfg.Agent.MCPAddr = "127.0.0.1:0"
	cfg.Agent.StartTimeout = time.Second
	cfg.Agent.MCPHeaderTimeout = time.Second
	cfg.Agent.MaxMessageBytes = 1 << 20
	cfg.Agent.MaxToolResultBytes = 1 << 20
	cfg.Permission.SummaryBytes = 200

	_, _, err := startAgent(context.Background(), cfg, buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t)), docs, nil)
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Errorf("err = %v; want one naming the command", err)
	}
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
//
// This walks the AST of run() to find an actual call to telemetry.Init,
// rather than searching source text, so the test asserts run() calls it
// regardless of where else that name appears in the file.
func TestCompositionRootInjectsATracer(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var run *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			run = fn
			break
		}
	}
	if run == nil {
		t.Fatal("run() not found in main.go")
	}

	var initPos, tracerPos token.Pos
	var sawWithTracer bool
	ast.Inspect(run, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "WithTracer" {
			sawWithTracer = true
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "telemetry" && sel.Sel.Name == "Init":
			initPos = call.Pos()
		case pkg.Name == "otel" && sel.Sel.Name == "Tracer":
			tracerPos = call.Pos()
		}
		return true
	})

	if !sawWithTracer {
		t.Error("run() never calls WithTracer; the runner will keep its no-op tracer")
	}
	if initPos == token.NoPos {
		t.Fatal("run() never calls telemetry.Init")
	}
	if tracerPos == token.NoPos {
		t.Fatal("run() never obtains a tracer via otel.Tracer")
	}
	if tracerPos < initPos {
		t.Error("the tracer is obtained before telemetry.Init installs a provider; it will be the no-op one")
	}
}

// runDecl returns run()'s declaration in main.go.
func runDecl(t *testing.T) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			return fset, fn
		}
	}
	t.Fatal("run() not found in main.go")
	return nil, nil
}

// callPositions returns where n calls pkg.name, or name when pkg is empty.
func callPositions(n ast.Node, pkg, name string) []token.Pos {
	var at []token.Pos
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if pkg == "" && f.Name == name {
				at = append(at, call.Pos())
			}
		case *ast.SelectorExpr:
			if id, ok := f.X.(*ast.Ident); ok && id.Name == pkg && f.Sel.Name == name {
				at = append(at, call.Pos())
			}
		}
		return true
	})
	return at
}

// Every surface shares the registry run() builds; a surface branch that
// built its own would stand up a second provider and a second tool set.
func TestBuildRegistryIsCalledOnce(t *testing.T) {
	n := 0
	for _, f := range mainFiles(t) {
		n += len(callPositions(f, "", "buildRegistry"))
	}
	if n != 1 {
		t.Errorf("package main calls buildRegistry %d times; want exactly once, shared by every surface", n)
	}
}

// mainFiles parses every non-test Go file in package main.
func mainFiles(t *testing.T) []*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	return files
}

// The lock is held before anything opens the store, so a second process is
// refused before it touches git.
func TestRunLocksTheStoreBeforeOpeningIt(t *testing.T) {
	_, run := runDecl(t)
	lock, open := callPositions(run, "pidlock", "Acquire"), callPositions(run, "gitdocs", "Open")
	if len(lock) != 1 || len(open) != 1 {
		t.Fatalf("run() calls pidlock.Acquire %d times and gitdocs.Open %d times; want one each", len(lock), len(open))
	}
	if lock[0] > open[0] {
		t.Error("run() opens the store before locking it")
	}
}

func TestCompositionRootReportsProgress(t *testing.T) {
	_, run := runDecl(t)
	var found bool
	ast.Inspect(run, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithProgress" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("run() never calls WithProgress; the CLI would show no step progress")
	}
}

func TestProgressPrinterWritesOneLinePerEvent(t *testing.T) {
	var b bytes.Buffer
	p := progressPrinter(&b)
	p(domain.StepEvent{StepID: "fetch", Tool: "http.request", Status: domain.StepStarted})
	p(domain.StepEvent{StepID: "fetch", Tool: "http.request", Status: domain.StepDone})
	p(domain.StepEvent{StepID: "judge", Tool: "judge.ask", Status: domain.StepFailed, Err: errors.New("boom")})
	want := "atlas: step fetch (http.request) started\n" +
		"atlas: step fetch (http.request) done\n" +
		"atlas: step judge (judge.ask) failed\n"
	if b.String() != want {
		t.Errorf("progress output =\n%q\nwant\n%q", b.String(), want)
	}
}

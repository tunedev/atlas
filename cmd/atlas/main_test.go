package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/feedsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/app"
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
	reg, _ := buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))

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
	r, _ := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	for _, name := range []string{"http.request", "model.complete", "judge.ask", "source.pull", "file.read", "file.text", "docs.put", "index.find", "docs.get", "quote.ground", "extract.run", "decision.record", "judge.each", "items.dedupe", "crawl.pull", "judge.outcome", "judge.calibrate", "decision.agreement", "judge.assess"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}

func TestBuildRegistryRegistersTheTailoringTools(t *testing.T) {
	docs, index := testStore(t)
	r, _ := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	for _, name := range []string{"text.spans", "span.resolve", "citations.judge", "claims.settle", "items.cite", "items.gather", "text.lines"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}

func TestBuildRegistryRegistersThePolicyAndStageTools(t *testing.T) {
	docs, index := testStore(t)
	base, _ := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	r := withPackEach(base, noop.NewTracerProvider().Tracer(""), index, io.Discard)
	for _, name := range []string{"policy.decide", "policy.suggest", "policy.add", "stage.declare", "stage.attach", "pack.each"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("registry has no %s", name)
		}
	}
}

// echo records that it ran and returns its input.
type echo struct{ calls *int }

func (echo) Name() string { return "echo" }

func (e echo) Invoke(_ context.Context, with map[string]string) (any, error) {
	*e.calls++
	return with, nil
}

// writePacks writes three packs into a temp directory: leaf runs echo,
// nested runs pack.each over leaf, and top runs pack.each over leaf.
func writePacks(t *testing.T) (leaf, nested, top string) {
	t.Helper()
	dir := t.TempDir()
	leaf = filepath.Join(dir, "leaf.yaml")
	nested = filepath.Join(dir, "nested.yaml")
	top = filepath.Join(dir, "top.yaml")
	each := func(name string) string {
		return "name: " + name + "\nsteps:\n  - id: each\n    tool: pack.each\n    with:\n" +
			"      pack: " + leaf + "\n      rows: '[{\"subject_id\":\"lamp-1\"}]'\n      match: stage=\n"
	}
	files := map[string]string{
		leaf:   "name: leaf\nsteps:\n  - id: say\n    tool: echo\n    with:\n      text: beam\n",
		nested: each("nested"),
		top:    each("top"),
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return leaf, nested, top
}

func TestPackEachChildCannotRunPackEach(t *testing.T) {
	_, index := testStore(t)
	leaf, nested, _ := writePacks(t)
	calls := 0
	reg := withPackEach(tools.NewRegistry(echo{calls: &calls}), noop.NewTracerProvider().Tracer(""), index, io.Discard)
	each, ok := reg.Lookup("pack.each")
	if !ok {
		t.Fatal("registry has no pack.each")
	}
	rows := `[{"subject_id":"lamp-1"}]`

	if _, err := each.Invoke(context.Background(), map[string]string{"pack": leaf, "rows": rows, "match": "stage="}); err != nil || calls != 1 {
		t.Fatalf("a child without pack.each: err = %v, echo calls = %d; want nil, 1", err, calls)
	}
	_, err := each.Invoke(context.Background(), map[string]string{"pack": nested, "rows": rows})
	if err == nil || !strings.Contains(err.Error(), `no tool named "pack.each"`) {
		t.Errorf("a child calling pack.each: err = %v; want no tool named pack.each", err)
	}
	if calls != 1 {
		t.Errorf("echo calls = %d; the nested leaf ran", calls)
	}
}

func TestPackEachTracesChildStepsUnderTheParent(t *testing.T) {
	_, index := testStore(t)
	_, _, top := writePacks(t)
	rec := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")
	calls := 0
	reg := withPackEach(tools.NewRegistry(echo{calls: &calls}), tracer, index, io.Discard)

	b, err := packfile.Load(top)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.NewRunner(reg).WithTracer(tracer).Run(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	parent, child := spans["tool.pack.each"], spans["blueprint.leaf"]
	if parent == nil || child == nil {
		t.Fatalf("spans = %v; want tool.pack.each and blueprint.leaf", slices.Collect(maps.Keys(spans)))
	}
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("the child pack's span is not a child of the parent's pack.each span")
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
	for _, name := range []string{"buildRegistry", "startAgent", "startRender", "withPackEach", "childRunner", "stageOf", "withAgent"} {
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

	reg, _ := buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	_, _, err := startAgent(context.Background(), cfg, reg, docs, nil)
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Errorf("err = %v; want one naming the command", err)
	}
}

// The agent is offered tools from the registry without pack.each, so a pack
// the agent wrote cannot run tools outside its allowlist.
func TestAgentIsNeverOfferedPackEach(t *testing.T) {
	docs, index := testStore(t)
	cfg := config.Config{}
	cfg.Agent.Command = filepath.Join(t.TempDir(), "no-such-agent")
	cfg.Agent.Tools = []string{"pack.each"}
	cfg.Agent.MCPAddr = "127.0.0.1:0"
	cfg.Agent.StartTimeout = time.Second
	cfg.Agent.MCPHeaderTimeout = time.Second
	cfg.Agent.MaxMessageBytes = 1 << 20
	cfg.Agent.MaxToolResultBytes = 1 << 20
	cfg.Permission.SummaryBytes = 200
	base, _ := buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))

	_, _, err := withAgent(context.Background(), cfg, base, noop.NewTracerProvider().Tracer(""), index, docs, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `no tool named "pack.each" to offer the agent`) {
		t.Errorf("err = %v; want pack.each refused as an agent tool", err)
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

func TestEgressTableMarksLoopbackAsLocal(t *testing.T) {
	cases := []struct {
		baseURL, endpoint string
		hosted            bool
	}{
		{"http://localhost:11434/v1", "http://localhost:11434", false},
		{"http://127.0.0.1:1", "http://127.0.0.1:1", false},
		{"http://[::1]:1", "http://[::1]:1", false},
		{"https://api.example.com/v1", "https://api.example.com", true},
		{"http://10.0.0.5:11434", "http://10.0.0.5:11434", true},
	}
	for _, tc := range cases {
		cfg := config.Config{}
		cfg.Model.BaseURL = tc.baseURL
		table := egressTable(cfg, []string{"model.complete"})
		if len(table) != 1 {
			t.Fatalf("%s: table = %v, want one row", tc.baseURL, table)
		}
		row := table[0]
		if row.Endpoint != tc.endpoint || row.Hosted != tc.hosted || !slices.Equal(row.Tools, []string{"model.complete"}) {
			t.Errorf("%s: row = %+v, want endpoint %s, hosted %v, tools [model.complete]", tc.baseURL, row, tc.endpoint, tc.hosted)
		}
	}
}

func TestAnUnusableModelURLIsAHostedPlaceholder(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.example.invalid:%zz/?key=sk-sentinel",
		"api.example.invalid/v1?key=sk-sentinel",
		"",
	} {
		row := modelEndpoint(baseURL, []string{"model.complete"})
		if row.Endpoint != "unparsed model endpoint" || !row.Hosted {
			t.Errorf("%q: row = %+v, want the hosted placeholder", baseURL, row)
		}
	}
}

func TestEgressTableAddsTheAgentAsHosted(t *testing.T) {
	cfg := config.Config{}
	cfg.Model.BaseURL = "http://localhost:11434/v1"
	cfg.Agent.Command = "some-agent"
	table := egressTable(cfg, nil)
	if len(table) != 2 {
		t.Fatalf("table = %v, want a model row and an agent row", table)
	}
	agent := table[1]
	if agent.Endpoint != "agent:some-agent" || !agent.Hosted || !slices.Equal(agent.Tools, []string{"agent.do"}) {
		t.Errorf("agent row = %+v", agent)
	}
}

// Every tool built over a model client must be in the modelTools literal,
// or the egress table would not name it. A call that builds a model client
// itself is not a tool.
func TestEgressTableCoversModelTools(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	clients := []string{"provider", "judge", "extractor"}
	fn := funcDecl(t, f, "buildRegistry")
	var lit *ast.CompositeLit
	builders := map[*ast.CallExpr]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		id, _ := assign.Lhs[0].(*ast.Ident)
		switch {
		case id == nil:
		case id.Name == "modelTools":
			lit, _ = assign.Rhs[0].(*ast.CompositeLit)
		case slices.Contains(clients, id.Name):
			if call, ok := assign.Rhs[0].(*ast.CallExpr); ok {
				builders[call] = true
			}
		}
		return true
	})
	if lit == nil || len(lit.Elts) == 0 {
		t.Fatal("buildRegistry has no modelTools composite literal")
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || builders[call] || !takesAny(call, clients) {
			return true
		}
		if call.Pos() < lit.Pos() || call.End() > lit.End() {
			t.Errorf("%s: a call over a model client sits outside modelTools", fset.Position(call.Pos()))
		}
		return true
	})
}

func takesAny(call *ast.CallExpr, names []string) bool {
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && slices.Contains(names, id.Name) {
			return true
		}
	}
	return false
}

func funcDecl(t *testing.T, f *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}

func TestBuildRegistryReportsModelToolNames(t *testing.T) {
	docs, index := testStore(t)
	_, names := buildRegistry(config.Config{}, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	want := []string{"citations.judge", "extract.run", "judge.ask", "judge.each", "model.complete"}
	got := slices.Sorted(slices.Values(names))
	if !slices.Equal(got, want) {
		t.Errorf("model tools = %v, want %v", got, want)
	}
}

// decisionsView is a view whose screen lists decision rows and whose action
// records one through the repo's decide pack, reached at decidePath.
const decisionsView = `title: Decisions
discloses: [decision notes]
screens:
  - id: days
    title: Days
    params: [subject]
    run: decisions.yaml
    show:
      - table:
          from: rows
          columns: [path]
    actions:
      - label: Record
        run: %s
        input:
          - name: reason
            text: true
        vars:
          subject_id: param.subject
          choice: const.apply
          reason: input.reason
  - id: wait
    title: Wait
    run: wait.yaml
`

const waitPack = `name: wait
steps:
  - id: held
    tool: test.wait
`

const decisionsPack = `name: decisions
steps:
  - id: rows
    tool: index.find
    with:
      kind: decision
`

// writeDecisionsView writes the decisions view and its screen pack into a
// temp dir and returns the view's path.
func writeDecisionsView(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	decide, err := filepath.Abs(filepath.Join("..", "..", "packs", "decide.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(dir, decide)
	if err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(dir, "decisions.ui.yaml")
	if err := os.WriteFile(view, []byte(fmt.Sprintf(decisionsView, rel)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "decisions.yaml"), []byte(decisionsPack), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wait.yaml"), []byte(waitPack), 0o600); err != nil {
		t.Fatal(err)
	}
	return view
}

// serveConfig is a -serve config over a fresh store, with the model at
// modelURL.
func serveConfig(t *testing.T, modelURL string) config.Config {
	t.Helper()
	cfg := config.Config{}
	cfg.Store.Root = t.TempDir()
	cfg.Store.IndexPath = filepath.Join(t.TempDir(), "index.db")
	cfg.Model.BaseURL = modelURL
	cfg.Model.Name = "a-model"
	cfg.Model.Timeout = time.Second
	cfg.Model.MaxBytes = 1 << 20
	cfg.Pack.FileMaxBytes = 1 << 20
	cfg.Web.Serve = true
	cfg.Web.Addr = "127.0.0.1:0"
	cfg.Web.Views = []string{writeDecisionsView(t)}
	cfg.Web.RunTimeout = 10 * time.Second
	cfg.Web.AskTimeout = time.Second
	cfg.Web.HeaderTimeout = time.Second
	return cfg
}

// waitTool blocks until its ctx is done.
type waitTool struct{}

func (waitTool) Name() string { return "test.wait" }

func (waitTool) Invoke(ctx context.Context, _ map[string]string) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// startServe runs serve over the real registry, plus test.wait, and the
// store in a goroutine the test owns, reads the startup lines, exchanges the
// token and returns a Connect JSON client, its Origin and a stop func that
// cancels serve and returns its result. Cleanup stops serve and fails the
// test unless it returns nil.
func startServe(t *testing.T, cfg config.Config) (uiv1.UIServiceClient, string, func() error) {
	t.Helper()
	return startServeTapped(t, cfg, io.Discard, http.DefaultTransport)
}

// startServeTapped is startServe with every byte serve writes to its out
// copied to out, and the client's requests sent through transport.
func startServeTapped(t *testing.T, cfg config.Config, out io.Writer, transport http.RoundTripper) (uiv1.UIServiceClient, string, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	docs, err := gitdocs.Open(ctx, cfg.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	index, err := sqlindex.Open(ctx, cfg.Store.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	registry, modelTools := buildRegistry(cfg, docs, index, feedsource.New(feedsource.Config{}), testCrawler(t))
	runner := app.NewRunner(registry.With(waitTool{}))

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, cfg, runner, web.NewAsker(cfg.Web.AskTimeout), egressTable(cfg, modelTools), pw)
		_ = pw.Close()
	}()
	copied := make(chan struct{})
	stop := sync.OnceValue(func() error {
		cancel()
		err := <-done
		<-copied
		return err
	})
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("serve returned %v; want nil", err)
		}
	})

	lines := bufio.NewScanner(pr)
	var startup []string
	for len(startup) < 2 && lines.Scan() {
		startup = append(startup, lines.Text())
		fmt.Fprintln(out, lines.Text())
	}
	go func() {
		_, _ = io.Copy(out, pr)
		close(copied)
	}()
	if len(startup) != 2 || startup[1] != "atlas: the CLI cannot use this store while the server runs; stop it with Ctrl-C" {
		t.Fatalf("startup lines = %q", startup)
	}
	serving, ok := strings.CutPrefix(startup[0], "atlas: serving ")
	if !ok {
		t.Fatalf("first startup line = %q", startup[0])
	}
	base, token, ok := strings.Cut(serving, "/#token=")
	if !ok || token == "" {
		t.Fatalf("no token in %q", startup[0])
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Transport: transport}
	req, err := http.NewRequest(http.MethodPost, base+"/session", strings.NewReader(`{"token":"`+token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("session exchange status %d; want 204", resp.StatusCode)
	}
	// No compression, so a tapped transport reads every message in the clear.
	return uiv1.NewUIServiceClient(client, base, connect.WithProtoJSON(), connect.WithAcceptCompression("gzip", nil, nil)), base, stop
}

func withOrigin[T any](msg *T, origin string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Origin", origin)
	return req
}

// runToEnd runs req and returns every event, failing the test on a stream
// error.
func runToEnd(t *testing.T, c uiv1.UIServiceClient, origin string, req *uiv1.RunRequest) []*uiv1.RunResponse {
	t.Helper()
	stream, err := c.Run(context.Background(), withOrigin(req, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var events []*uiv1.RunResponse
	for stream.Receive() {
		events = append(events, stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("run %s/%d: %v", req.Screen, req.Action, err)
	}
	return events
}

// decisionRows runs the days screen and returns its rows' fields.
func decisionRows(t *testing.T, c uiv1.UIServiceClient, origin string) []map[string]string {
	t.Helper()
	events := runToEnd(t, c, origin, &uiv1.RunRequest{View: "decisions", Screen: "days", Action: -1, Params: map[string]string{"subject": "s-1"}})
	done := events[len(events)-1].GetDone()
	if done == nil {
		t.Fatalf("screen run ended with %v; want Done", events[len(events)-1])
	}
	var state struct {
		Rows struct {
			Rows []struct {
				Fields map[string]string `json:"fields"`
			} `json:"rows"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(done.StateJson), &state); err != nil {
		t.Fatalf("state %s: %v", done.StateJson, err)
	}
	rows := make([]map[string]string, len(state.Rows.Rows))
	for i, r := range state.Rows.Rows {
		rows[i] = r.Fields
	}
	return rows
}

// failingModel is a model endpoint that answers 500 to everything.
func failingModel(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/v1"
}

func TestServeEndToEnd(t *testing.T) {
	c, origin, _ := startServe(t, serveConfig(t, failingModel(t)))

	views, err := c.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(views.Msg.ViewsJson, `"id":"decisions"`) {
		t.Errorf("views = %s; want the decisions view", views.Msg.ViewsJson)
	}
	if len(views.Msg.Egress) != 1 || views.Msg.Egress[0].Hosted {
		t.Errorf("egress = %v; want the model as one local endpoint", views.Msg.Egress)
	}

	if rows := decisionRows(t, c, origin); len(rows) != 0 {
		t.Fatalf("rows before the action = %v; want none", rows)
	}

	events := runToEnd(t, c, origin, &uiv1.RunRequest{
		View: "decisions", Screen: "days", Action: 0,
		Params: map[string]string{"subject": "s-1"},
		Inputs: map[string]string{"reason": "fits"},
	})
	if events[len(events)-1].GetDone() == nil {
		t.Fatalf("action ended with %v; want Done", events[len(events)-1])
	}

	rows := decisionRows(t, c, origin)
	if len(rows) != 1 || rows[0]["subject_id"] != "s-1" || rows[0]["decision"] != "apply" {
		t.Errorf("rows after the action = %v; want one apply decision for s-1", rows)
	}
}

// A hosted model endpoint gates every run until it is acknowledged.
func TestServeGatesAHostedEndpoint(t *testing.T) {
	c, origin, _ := startServe(t, serveConfig(t, "https://api.example.invalid/v1"))
	screen := &uiv1.RunRequest{View: "decisions", Screen: "days", Action: -1, Params: map[string]string{"subject": "s-1"}}

	events := runToEnd(t, c, origin, screen)
	if len(events) != 1 || events[0].GetNeedsAcknowledgement() == nil {
		t.Fatalf("gated run events = %v; want only NeedsAcknowledgement", events)
	}
	if got := events[0].GetNeedsAcknowledgement().Endpoints; !slices.Equal(got, []string{"https://api.example.invalid"}) {
		t.Errorf("endpoints = %v", got)
	}

	if _, err := c.Acknowledge(context.Background(), withOrigin(&uiv1.AcknowledgeRequest{Endpoint: "https://api.example.invalid"}, origin)); err != nil {
		t.Fatal(err)
	}
	events = runToEnd(t, c, origin, screen)
	if events[len(events)-1].GetDone() == nil {
		t.Errorf("acknowledged run ended with %v; want Done", events[len(events)-1])
	}
}

// Ctrl-C ends serve promptly even while a run's stream is open: the run's
// ctx is cancelled rather than waited out.
func TestServeStopsWithARunInFlight(t *testing.T) {
	c, origin, stop := startServe(t, serveConfig(t, failingModel(t)))
	stream, err := c.Run(context.Background(), withOrigin(&uiv1.RunRequest{View: "decisions", Screen: "wait", Action: -1}, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	held := false
	for !held && stream.Receive() {
		step := stream.Msg().GetStep()
		held = step != nil && step.StepId == "held"
	}
	if !held {
		t.Fatalf("the run ended before its held step started: %v", stream.Err())
	}

	start := time.Now()
	if err := stop(); err != nil {
		t.Fatalf("serve returned %v with a run in flight; want nil", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("serve took %s to stop; want well under the %s shutdown budget", took, serveShutdownBudget)
	}
}

// termprompt.New starts a goroutine that reads stdin, so run() builds it
// only inside the branch that starts an agent; a plain pack run leaves
// piped stdin alone.
func TestTerminalPromptIsBuiltOnlyForAnAgent(t *testing.T) {
	_, run := runDecl(t)
	calls := callPositions(run, "termprompt", "New")
	if len(calls) != 1 {
		t.Fatalf("run() calls termprompt.New %d times; want once", len(calls))
	}
	var agent *ast.IfStmt
	ast.Inspect(run, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || agent != nil {
			return true
		}
		if bin, ok := ifs.Cond.(*ast.BinaryExpr); ok {
			if sel, ok := bin.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Command" {
				agent = ifs
			}
		}
		return true
	})
	if agent == nil {
		t.Fatal("run() has no cfg.Agent.Command branch")
	}
	if calls[0] < agent.Body.Pos() || calls[0] > agent.Body.End() {
		t.Error("run() builds termprompt.New outside the agent branch; it would read stdin on every run")
	}
}

// TestPackEachPrintsChildProgressUnderTheParent runs a parent pack whose
// pack.each step runs a child: each child step's line sits inside the
// parent step's lines, indented and prefixed with the child pack's path.
func TestPackEachPrintsChildProgressUnderTheParent(t *testing.T) {
	_, index := testStore(t)
	leaf, _, top := writePacks(t)
	var b bytes.Buffer
	calls := 0
	reg := withPackEach(tools.NewRegistry(echo{calls: &calls}), noop.NewTracerProvider().Tracer(""), index, &b)

	bp, err := packfile.Load(top)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.NewRunner(reg).WithProgress(progressPrinter(&b)).Run(context.Background(), bp); err != nil {
		t.Fatal(err)
	}
	want := "atlas: step each (pack.each) started\n" +
		"atlas:   " + leaf + ": step say (echo) started\n" +
		"atlas:   " + leaf + ": step say (echo) done\n" +
		"atlas: step each (pack.each) done\n"
	if b.String() != want {
		t.Errorf("progress output =\n%s\nwant\n%s", b.String(), want)
	}
}

// Every shipped view file loads against the real packs, so every run it
// names resolves and every var it binds is one that pack declares.
func TestShippedViewsLoad(t *testing.T) {
	paths, err := filepath.Glob("../../packs/*.ui.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped view files found")
	}
	if _, err := web.LoadViews(paths, packfile.Load); err != nil {
		t.Fatal(err)
	}
}

// tap records bytes written to it and, as a RoundTripper, every response's
// status line, headers and body as the client reads them.
type tap struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (r *tap) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.b.Write(p)
}

func (r *tap) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.b.String()
}

func (r *tap) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(r, "%s %s\n", resp.Proto, resp.Status)
	r.mu.Lock()
	_ = resp.Header.Write(&r.b)
	r.mu.Unlock()
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(resp.Body, r), resp.Body}
	return resp, nil
}

// tapSlog points slog's default logger at a new tap until the test ends.
func tapSlog(t *testing.T) *tap {
	t.Helper()
	logs := &tap{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return logs
}

// echoingModel is a model endpoint that answers 401 with the request's
// bearer token in the body, the way hosted gateways do. hits counts calls.
func echoingModel(t *testing.T, hits *atomic.Int32) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, token)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/v1"
}

const probeView = `title: Probe
screens:
  - id: ask
    title: Ask
    run: probe.yaml
`

const probePack = `name: probe
steps:
  - id: reply
    tool: model.complete
    with:
      user: say hello
`

// withProbeView adds a view whose one screen calls model.complete, and a
// files root holding one file, to cfg.
func withProbeView(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.ui.yaml"), []byte(probeView), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.yaml"), []byte(probePack), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Web.Views = append(cfg.Web.Views, filepath.Join(dir, "probe.ui.yaml"))
	cfg.Web.FilesRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.Web.FilesRoot, "a.txt"), []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// storeHistory is every commit in the store with its patch, plus the raw
// index file.
func storeHistory(t *testing.T, cfg config.Config) string {
	t.Helper()
	log, err := exec.Command("git", "-C", cfg.Store.Root, "log", "-p", "--all").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, log)
	}
	index, err := os.ReadFile(cfg.Store.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(log) + string(index)
}

// acknowledgeFirst acknowledges the first egress endpoint Views reports and
// returns it.
func acknowledgeFirst(t *testing.T, c uiv1.UIServiceClient, origin string) string {
	t.Helper()
	views, err := c.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := views.Msg.Egress[0].Endpoint
	if _, err := c.Acknowledge(context.Background(), withOrigin(&uiv1.AcknowledgeRequest{Endpoint: endpoint}, origin)); err != nil {
		t.Fatalf("Acknowledge %s: %v", endpoint, err)
	}
	return endpoint
}

// The model API key never reaches a response, serve's output, a log line,
// the store's history or the index, even when the model echoes it.
func TestNoResponseCarriesTheAPIKey(t *testing.T) {
	sentinel := "sk-" + web.NewToken()[:40]
	scan := func(t *testing.T, surfaces map[string]string) {
		t.Helper()
		for name, text := range surfaces {
			if strings.Contains(text, sentinel) {
				t.Errorf("the API key reached %s", name)
			}
		}
	}

	t.Run("a model that echoes the key", func(t *testing.T) {
		logs := tapSlog(t)
		var hits atomic.Int32
		cfg := withProbeView(t, serveConfig(t, echoingModel(t, &hits)))
		cfg.Model.APIKey = sentinel
		wire, out := &tap{}, &tap{}
		c, origin, stop := startServeTapped(t, cfg, out, wire)
		ctx := context.Background()

		if _, err := c.Views(ctx, withOrigin(&uiv1.ViewsRequest{}, origin)); err != nil {
			t.Fatal(err)
		}
		stream, err := c.Run(ctx, withOrigin(&uiv1.RunRequest{View: "probe", Screen: "ask", Action: -1}, origin))
		if err != nil {
			t.Fatal(err)
		}
		for stream.Receive() {
		}
		runErr := stream.Err()
		_ = stream.Close()
		if hits.Load() == 0 || runErr == nil || !strings.Contains(runErr.Error(), "401") {
			t.Fatalf("the run did not fail at the model: hits %d, err %v", hits.Load(), runErr)
		}
		if _, err := c.Answer(ctx, withOrigin(&uiv1.AnswerRequest{Id: "feedface", Allow: true}, origin)); connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("Answer with an unknown id: %v; want NotFound", err)
		}
		endpoint := acknowledgeFirst(t, c, origin)
		sum := sha256.Sum256([]byte(endpoint))
		record := "atlas/egress/" + hex.EncodeToString(sum[:])[:16] + ".json"
		// Document bodies travel base64-encoded, so they are scanned decoded.
		var documents strings.Builder
		for _, doc := range []*uiv1.DocumentRequest{{Source: "record", Path: record}, {Source: "file", Path: "a.txt"}} {
			resp, err := c.Document(ctx, withOrigin(doc, origin))
			if err != nil {
				t.Fatalf("Document %s %s: %v", doc.Source, doc.Path, err)
			}
			documents.Write(resp.Msg.Body)
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(wire.String(), "Incorrect API key provided: [redacted]") {
			t.Error("the tapped responses do not carry the model's redacted error in the clear")
		}
		scan(t, map[string]string{"a response": wire.String(), "a document body": documents.String(), "serve's output": out.String(), "the log": logs.String(), "the store": storeHistory(t, cfg)})
	})

	for name, baseURL := range map[string]string{
		"a hosted URL carrying the key":       "https://u:" + sentinel + "@api.example.invalid/v1?key=" + sentinel,
		"an unparseable URL carrying the key": "https://api.example.invalid:%zz/?key=" + sentinel,
	} {
		t.Run(name, func(t *testing.T) {
			logs := tapSlog(t)
			cfg := withProbeView(t, serveConfig(t, baseURL))
			cfg.Model.APIKey = sentinel
			wire, out := &tap{}, &tap{}
			c, origin, stop := startServeTapped(t, cfg, out, wire)

			events := runToEnd(t, c, origin, &uiv1.RunRequest{View: "probe", Screen: "ask", Action: -1})
			if len(events) != 1 || events[0].GetNeedsAcknowledgement() == nil {
				t.Fatalf("run events = %v; want only NeedsAcknowledgement", events)
			}
			acknowledgeFirst(t, c, origin)
			if err := stop(); err != nil {
				t.Fatal(err)
			}

			scan(t, map[string]string{"a response": wire.String(), "serve's output": out.String(), "the log": logs.String(), "the store": storeHistory(t, cfg)})
		})
	}
}

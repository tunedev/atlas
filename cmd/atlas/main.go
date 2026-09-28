// Command atlas runs a pack. This is the only file that knows every concrete
// type, and it knows nothing about what any pack does.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"
	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/outbound/acpagent"
	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/feedsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/termprompt"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
	"github.com/tunedev/atlas/internal/pidlock"
	"github.com/tunedev/atlas/internal/telemetry"
)

// storeLockName is the lock file, inside Store.Root, that keeps one atlas
// process at a time working in a store.
const storeLockName = ".atlas.lock"

// progressPrinter writes one line to w as each step starts and finishes, so
// a long step reads as running rather than hung. A failure's cause is left
// to the error run() returns.
func progressPrinter(w io.Writer) func(domain.StepEvent) {
	return stepPrinter(w, "atlas:")
}

// childProgressPrinter is progressPrinter for a child pack run by pack.each:
// each line is indented and names the child's pack path, so it reads as part
// of the parent step that ran it.
func childProgressPrinter(w io.Writer, pack string) func(domain.StepEvent) {
	return stepPrinter(w, "atlas:   "+pack+":")
}

// stepPrinter writes one line per step event to w, after prefix.
func stepPrinter(w io.Writer, prefix string) func(domain.StepEvent) {
	return func(e domain.StepEvent) {
		fmt.Fprintf(w, "%s step %s (%s) %s\n", prefix, e.StepID, e.Tool, e.Status)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}

// buildRegistry builds every shipped tool and returns, beside the registry,
// the names of the tools that send data to the model endpoint.
func buildRegistry(cfg config.Config, docs ports.Docs, index ports.Index, source ports.Source, crawler *crawlsource.Crawler) (tools.Registry, []string) {
	provider := openaiprov.New(openaiprov.Config{
		Name:     cfg.Model.Name,
		BaseURL:  cfg.Model.BaseURL,
		Model:    cfg.Model.Name,
		APIKey:   cfg.Model.APIKey,
		Timeout:  cfg.Model.Timeout,
		MaxBytes: cfg.Model.MaxBytes,
	})
	judge := app.NewJudge(provider, app.JudgeConfig{
		Temperature:   cfg.Judge.Temperature,
		Seed:          cfg.Judge.Seed,
		TopLogProbs:   cfg.Judge.TopLogProbs,
		MaxTokens:     cfg.Judge.MaxTokens,
		ContextTokens: cfg.Judge.ContextTokens,
	})
	extractor := app.NewExtractor(provider, app.ExtractorConfig{
		Temperature: cfg.Extract.Temperature,
		MaxTokens:   cfg.Extract.MaxTokens,
	})
	modelTools := []ports.Tool{
		tools.NewModel(provider),
		tools.NewJudge(judge, docs, index),
		tools.NewExtract(extractor),
		tools.NewCitationsJudge(judge, docs, index),
		tools.NewJudgeEach(source, judge, docs, index, cfg.Model.Name, cfg.Feed.StaleAfter, slog.Default()),
	}
	registry := tools.NewRegistry(append(modelTools,
		tools.NewHTTP(cfg.Pack.HTTPTimeout, cfg.Pack.HTTPMaxBytes),
		tools.NewFileRead(cfg.Pack.FileMaxBytes),
		tools.NewFileText(cfg.Pack.FileMaxBytes),
		tools.NewDocsPut(docs, index),
		tools.NewIndexFind(index),
		tools.NewDocsGet(docs, cfg.Pack.FileMaxBytes),
		tools.NewQuoteGround(),
		tools.NewDecision(docs, index),
		tools.NewSourcePull(source, cfg.Feed.StaleAfter, slog.Default()),
		tools.NewTextSpans(cfg.Pack.FileMaxBytes),
		tools.NewSpanResolve(),
		tools.NewClaimsSettle(),
		tools.NewItemsCite(),
		tools.NewItemsGather(),
		tools.NewTextLines(),
		tools.NewDedupe(),
		tools.NewCrawlPull(crawler, slog.Default()),
		tools.NewOutcome(docs, index),
		tools.NewCalibrate(docs, index),
		tools.NewAgreement(index),
		tools.NewJudgeAssess(docs),
		tools.NewPolicyDecide(docs),
		tools.NewPolicySuggest(index),
		tools.NewPolicyAdd(docs, index),
		tools.NewStageDeclare(docs, index),
		tools.NewStageAttach(index),
	)...)
	return registry, toolNames(modelTools)
}

func toolNames(list []ports.Tool) []string {
	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name()
	}
	return names
}

// egressTable names each endpoint the registry sends data to. The model
// endpoint is hosted unless its host is localhost or a loopback IP literal;
// an agent is always hosted, since atlas cannot see where it sends data.
func egressTable(cfg config.Config, modelTools []string) []web.Endpoint {
	table := []web.Endpoint{modelEndpoint(cfg.Model.BaseURL, modelTools)}
	if cfg.Agent.Command != "" {
		table = append(table, web.Endpoint{Endpoint: "agent:" + cfg.Agent.Command, Hosted: true, Tools: []string{"agent.do"}})
	}
	return table
}

// unparsedModelEndpoint names a model base URL with no usable scheme and host.
// The raw URL is never shown, since it can carry credentials.
const unparsedModelEndpoint = "unparsed model endpoint"

// modelEndpoint is the egress row for the model: scheme and host only, hosted
// unless the host is loopback. A URL without both is the hosted placeholder.
func modelEndpoint(baseURL string, modelTools []string) web.Endpoint {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return web.Endpoint{Endpoint: unparsedModelEndpoint, Hosted: true, Tools: modelTools}
	}
	return web.Endpoint{Endpoint: u.Scheme + "://" + u.Host, Hosted: !loopbackHost(u.Hostname()), Tools: modelTools}
}

// loopbackHost reports whether host is localhost or a loopback IP literal.
// A name is never resolved.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// startRender builds render.run over the configured typst binary. A binary
// that is configured but not installed fails here, before any pack runs.
func startRender(cfg config.Config) (ports.Tool, error) {
	conv, err := typstconv.New(typstconv.Config{
		Bin:      cfg.Render.TypstPath,
		Timeout:  cfg.Render.Timeout,
		MaxBytes: cfg.Render.MaxBytes,
	})
	if err != nil {
		return nil, err
	}
	return tools.NewRender(conv, cfg.Render.MaxBytes), nil
}

// startAgent launches the configured agent, offers it the configured tools
// from base over MCP, and returns the agent.do tool with a function that
// stops both. base is the registry without agent.do, so the agent cannot
// reach itself. human decides the tool calls the permission rules ask about.
func startAgent(ctx context.Context, cfg config.Config, base ports.Registry, docs ports.Docs, human ports.Permission) (ports.Tool, func() error, error) {
	perm := app.NewPermissionPolicy(permissionRules(cfg.Permission.Rules), human)

	var srv *http.Server
	var server *acpagent.MCPServer
	if cfg.Agent.Tools != nil {
		token := mcpserve.NewToken()
		handler, err := mcpserve.NewHandler(base, perm, mcpserve.Config{
			Tools:          cfg.Agent.Tools,
			MaxResultBytes: cfg.Agent.MaxToolResultBytes,
			SummaryBytes:   cfg.Permission.SummaryBytes,
			Token:          token,
			CallTimeout:    cfg.Agent.TurnTimeout,
		})
		if err != nil {
			return nil, nil, err
		}
		var url string
		srv, url, err = mcpserve.Listen(cfg.Agent.MCPAddr, handler, cfg.Agent.MCPHeaderTimeout)
		if err != nil {
			return nil, nil, err
		}
		server = &acpagent.MCPServer{Name: mcpserve.ServerName, URL: url, Headers: mcpserve.AuthHeader(token)}
	}

	startCtx, cancel := context.WithTimeout(ctx, cfg.Agent.StartTimeout)
	defer cancel()
	client, err := acpagent.New(startCtx, acpagent.Config{
		Command:         cfg.Agent.Command,
		Args:            cfg.Agent.Args,
		Env:             config.AgentEnv(os.Environ()),
		Stderr:          os.Stderr,
		MaxMessageBytes: cfg.Agent.MaxMessageBytes,
		SummaryBytes:    cfg.Permission.SummaryBytes,
		MCP:             server,
		WaitDelay:       cfg.Agent.CloseTimeout,
	}, perm)
	if err != nil {
		if srv != nil {
			_ = srv.Close()
		}
		return nil, nil, err
	}

	stop := func() error {
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Agent.CloseTimeout)
		defer cancel()
		err := client.Close(stopCtx)
		if srv != nil {
			err = errors.Join(err, srv.Shutdown(stopCtx))
		}
		return err
	}
	tool := tools.NewAgent(client, docs, os.Stderr, tools.AgentSettings{
		Command:         cfg.Agent.Command,
		Args:            cfg.Agent.Args,
		ProtocolVersion: client.ProtocolVersion(),
		WorkDir:         cfg.Agent.WorkDir,
		Timeout:         cfg.Agent.TurnTimeout,
	})
	return tool, stop, nil
}

// permissionRules converts configured rules into the policy's own type.
func permissionRules(rules []config.PermissionRule) []app.PermissionRule {
	out := make([]app.PermissionRule, len(rules))
	for i, r := range rules {
		out[i] = app.PermissionRule{ToolName: r.ToolName, Kind: r.Kind, Decision: ports.PermissionDecision(r.Decision)}
	}
	return out
}

func run() error {
	// A signal cancels ctx instead of killing the process, so the deferred
	// stops below run; the agent's own process group gets no signal from
	// the terminal and is ended by its stop.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}

	// One process at a time works in a store; the lock is taken before
	// anything opens it, and a lock left by a process that has exited is
	// reclaimed.
	if err := os.MkdirAll(cfg.Store.Root, 0o755); err != nil {
		return fmt.Errorf("store root: %w", err)
	}
	lock, err := pidlock.Acquire(filepath.Join(cfg.Store.Root, storeLockName))
	if err != nil {
		return err
	}
	if pid := lock.Reclaimed(); pid != 0 {
		fmt.Fprintf(os.Stderr, "atlas: reclaimed stale lock left by pid %d\n", pid)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		}
	}()

	shutdown, err := telemetry.Init(ctx, cfg)
	if err != nil {
		return err
	}
	// Bounded by the same export timeout the exporter itself uses, so
	// shutdown cannot outlive that budget on an unreachable collector.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.OTel.ExportTimeout)
		defer cancel()
		_ = shutdown(shutdownCtx)
	}()

	// telemetry.Init has already installed the tracer provider, so the
	// tracer obtained here is the real one when tracing is enabled and the
	// SDK no-op otherwise. Obtaining it before Init runs would capture the
	// no-op provider that is installed at startup.
	tracer := otel.Tracer("github.com/tunedev/atlas")

	// -serve has no pack; its runs name packs through the view files.
	var blueprint domain.Blueprint
	if !cfg.Web.Serve {
		blueprint, err = packfile.Load(cfg.Pack.Path)
		if err != nil {
			return err
		}
		blueprint, err = blueprint.WithVars(cfg.Pack.Vars)
		if err != nil {
			return err
		}
	}

	docs, err := gitdocs.Open(ctx, cfg.Store.Root)
	if err != nil {
		return err
	}

	index, err := sqlindex.Open(ctx, cfg.Store.IndexPath)
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()

	source := feedsource.New(feedsource.Config{
		RemoteURL:   cfg.Feed.RemoteURL,
		Ref:         cfg.Feed.Ref,
		CachePath:   cfg.Feed.CachePath,
		PullTimeout: cfg.Feed.PullTimeout,
	})

	// One Crawler for the process, so every crawl.pull shares its robots.txt
	// cache and per-host pacing.
	crawler, err := crawlsource.NewCrawler(crawlsource.Config{
		UserAgent:     cfg.Crawl.UserAgent,
		Delay:         cfg.Crawl.Delay,
		Timeout:       cfg.Crawl.Timeout,
		PullTimeout:   cfg.Crawl.PullTimeout,
		MaxBytes:      cfg.Crawl.MaxBytes,
		Retries:       cfg.Crawl.Retries,
		CacheDir:      cfg.Crawl.CacheDir,
		Render:        cfg.Crawl.Render,
		RenderTimeout: cfg.Crawl.RenderTimeout,
	})
	if err != nil {
		return err
	}

	registry, modelTools := buildRegistry(cfg, docs, index, source, crawler)

	if cfg.Render.TypstPath != "" {
		render, err := startRender(cfg)
		if err != nil {
			return err
		}
		registry = registry.With(render)
	}

	var asker *web.Asker
	if cfg.Web.Serve {
		asker = web.NewAsker(cfg.Web.AskTimeout)
	}

	if cfg.Agent.Command != "" {
		// The agent's permission asks go to the browser in -serve mode and
		// to the terminal otherwise; the terminal prompt reads stdin, so it
		// exists only when an agent can ask.
		var human ports.Permission
		if cfg.Web.Serve {
			human = asker
		} else {
			human = termprompt.New(os.Stdin, os.Stderr)
		}
		var stop func() error
		registry, stop, err = withAgent(ctx, cfg, registry, tracer, index, docs, human, os.Stderr)
		if err != nil {
			return err
		}
		defer func() {
			if err := stop(); err != nil {
				fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
			}
		}()
	} else {
		registry = withPackEach(registry, tracer, index, os.Stderr)
	}

	runner := app.NewRunner(registry).WithTracer(tracer)

	if cfg.Web.Serve {
		return serve(ctx, cfg, runner, asker, egressTable(cfg, modelTools), os.Stderr)
	}

	state, err := runner.WithProgress(progressPrinter(os.Stderr)).Run(ctx, blueprint)
	if err != nil {
		return err
	}

	// The harness cannot format a result it does not understand, so it prints
	// each step's output as JSON and lets the reader make sense of it.
	out, err := json.MarshalIndent(state.Outputs(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// serveShutdownBudget bounds how long serve waits for open requests to
// finish once ctx is done.
const serveShutdownBudget = 5 * time.Second

// serve loads the view files and serves the web UI on cfg.Web.Addr until
// ctx is done, which also cancels every open request. It prints the URL carrying the startup token to out, once.
func serve(ctx context.Context, cfg config.Config, runner *app.Runner, asker *web.Asker, egress []web.Endpoint, out io.Writer) error {
	views, err := web.LoadViews(cfg.Web.Views, packfile.Load)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Web.Addr)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	host := ln.Addr().String()
	token := web.NewToken()
	ui, err := web.New(web.Config{RunTimeout: cfg.Web.RunTimeout, FilesRoot: cfg.Web.FilesRoot}, web.Deps{
		Views:  views,
		Load:   packfile.Load,
		Runner: runner,
		Asker:  asker,
		Egress: egress,
		Server: map[string]string{"store_root": cfg.Store.Root, "files_root": cfg.Web.FilesRoot},
	}, token, host)
	if err != nil {
		_ = ln.Close()
		return err
	}

	fmt.Fprintf(out, "atlas: serving http://%s/#token=%s\n", host, token)
	fmt.Fprintln(out, "atlas: the CLI cannot use this store while the server runs; stop it with Ctrl-C")

	// Every request's ctx derives from ctx, so a signal ends open runs and
	// asks instead of leaving Shutdown to wait them out.
	srv := &http.Server{
		Handler:           ui.Handler(),
		ReadHeaderTimeout: cfg.Web.HeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownBudget)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("serve: shutdown: %w", err)
	}
	<-served
	return nil
}

// withAgent starts the agent over base and returns the runner's registry:
// base with pack.each and agent.do added. The agent is offered tools from
// base only, so it never reaches pack.each, whose child runs would skip the
// agent's per-tool permission check. human decides the tool calls the
// permission rules ask about.
func withAgent(ctx context.Context, cfg config.Config, base tools.Registry, tracer trace.Tracer, index ports.Index, docs ports.Docs, human ports.Permission, progress io.Writer) (tools.Registry, func() error, error) {
	runner := withPackEach(base, tracer, index, progress)
	agentTool, stop, err := startAgent(ctx, cfg, base, docs, human)
	if err != nil {
		return nil, nil, err
	}
	return runner.With(agentTool), stop, nil
}

// withPackEach returns registry with pack.each added. Its child runs see
// registry as given, which holds neither pack.each nor agent.do, so nesting
// stops at one level and a child pack never reaches the agent. Child steps
// report progress to progress.
func withPackEach(registry tools.Registry, tracer trace.Tracer, index ports.Index, progress io.Writer) tools.Registry {
	return registry.With(tools.NewPackEach(childRunner(registry, tracer, progress), stageOf(index)))
}

// childRunner runs one pack over registry, with vars overriding its own, so
// pack.each can run a pack per row; registry never holds pack.each.
func childRunner(registry ports.Registry, tracer trace.Tracer, progress io.Writer) tools.RunPack {
	return func(ctx context.Context, path string, vars map[string]string) error {
		b, err := packfile.Load(path)
		if err != nil {
			return err
		}
		if b, err = b.WithVars(vars); err != nil {
			return err
		}
		_, err = app.NewRunner(registry).WithTracer(tracer).WithProgress(childProgressPrinter(progress, path)).Run(ctx, b)
		return err
	}
}

// stageOf reads a subject's current stage from index.
func stageOf(index ports.Index) tools.StageOf {
	return func(ctx context.Context, subjectID string) (string, error) {
		return app.CurrentStage(ctx, index, subjectID)
	}
}

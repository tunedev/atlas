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

	"go.opentelemetry.io/otel"

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
	return func(e domain.StepEvent) {
		fmt.Fprintf(w, "atlas: step %s (%s) %s\n", e.StepID, e.Tool, e.Status)
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

func modelEndpoint(baseURL string, modelTools []string) web.Endpoint {
	u, err := url.Parse(baseURL)
	if err != nil {
		return web.Endpoint{Endpoint: baseURL, Hosted: true, Tools: modelTools}
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

	blueprint, err := packfile.Load(cfg.Pack.Path)
	if err != nil {
		return err
	}
	blueprint, err = blueprint.WithVars(cfg.Pack.Vars)
	if err != nil {
		return err
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

	registry, _ := buildRegistry(cfg, docs, index, source, crawler)

	if cfg.Render.TypstPath != "" {
		render, err := startRender(cfg)
		if err != nil {
			return err
		}
		registry = registry.With(render)
	}

	if cfg.Agent.Command != "" {
		agentTool, stop, err := startAgent(ctx, cfg, registry, docs, termprompt.New(os.Stdin, os.Stderr))
		if err != nil {
			return err
		}
		defer func() {
			if err := stop(); err != nil {
				fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
			}
		}()
		registry = registry.With(agentTool)
	}

	// telemetry.Init has already installed the tracer provider, so the
	// tracer obtained here is the real one when tracing is enabled and the
	// SDK no-op otherwise. Obtaining it before Init runs would capture the
	// no-op provider that is installed at startup.
	tracer := otel.Tracer("github.com/tunedev/atlas")
	runner := app.NewRunner(registry).WithTracer(tracer).WithProgress(progressPrinter(os.Stderr))

	state, err := runner.Run(ctx, blueprint)
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

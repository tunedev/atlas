// Command atlas runs a pack. This is the only file that knows every concrete
// type, and it knows nothing about what any pack does.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"go.opentelemetry.io/otel"

	"github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"
	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/outbound/acpagent"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
	"github.com/tunedev/atlas/internal/adapters/outbound/termprompt"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
	"github.com/tunedev/atlas/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}

func buildRegistry(cfg config.Config, docs ports.Docs, index ports.Index) tools.Registry {
	provider := openaiprov.New(openaiprov.Config{
		Name:     cfg.Model.Name,
		BaseURL:  cfg.Model.BaseURL,
		Model:    cfg.Model.Name,
		APIKey:   cfg.Model.APIKey,
		Timeout:  cfg.Model.Timeout,
		MaxBytes: cfg.Model.MaxBytes,
	})
	judge := app.NewJudge(provider, app.JudgeConfig{
		Temperature: cfg.Judge.Temperature,
		Seed:        cfg.Judge.Seed,
		TopLogProbs: cfg.Judge.TopLogProbs,
		MaxTokens:   cfg.Judge.MaxTokens,
	})
	return tools.NewRegistry(
		tools.NewHTTP(cfg.Pack.HTTPTimeout, cfg.Pack.HTTPMaxBytes),
		tools.NewModel(provider),
		tools.NewJudge(judge, docs, index),
	)
}

// startAgent launches the configured agent, offers it the configured tools
// from base over MCP, and returns the agent.do tool with a function that
// stops both. base is the registry without agent.do, so the agent cannot
// reach itself.
func startAgent(ctx context.Context, cfg config.Config, base ports.Registry, docs ports.Docs) (ports.Tool, func() error, error) {
	perm := app.NewPermissionPolicy(permissionRules(cfg.Permission.Rules), termprompt.New(os.Stdin, os.Stderr))

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
	ctx := context.Background()

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}

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

	docs, err := gitdocs.Open(ctx, cfg.Store.Root)
	if err != nil {
		return err
	}

	index, err := sqlindex.Open(ctx, cfg.Store.IndexPath)
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()

	registry := buildRegistry(cfg, docs, index)

	if cfg.Agent.Command != "" {
		agentTool, stop, err := startAgent(ctx, cfg, registry, docs)
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
	runner := app.NewRunner(registry).WithTracer(tracer)

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

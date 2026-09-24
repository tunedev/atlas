// Command atlas runs a pack. This is the only file that knows every concrete
// type, and it knows nothing about what any pack does.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/adapters/outbound/sqlindex"
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
	extractor := app.NewExtractor(provider, app.ExtractorConfig{
		Temperature: cfg.Extract.Temperature,
		MaxTokens:   cfg.Extract.MaxTokens,
	})
	return tools.NewRegistry(
		tools.NewHTTP(cfg.Pack.HTTPTimeout, cfg.Pack.HTTPMaxBytes),
		tools.NewModel(provider),
		tools.NewJudge(judge, docs, index),
		tools.NewFileRead(cfg.Pack.FileMaxBytes),
		tools.NewDocsPut(docs, index),
		tools.NewQuoteGround(),
		tools.NewExtract(extractor),
	)
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

	registry := buildRegistry(cfg, docs, index)

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

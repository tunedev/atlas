// Package telemetry wires OpenTelemetry's trace pipeline to an OTLP gRPC
// endpoint and installs the tracer provider as the process-wide global. It is
// called once, from cmd/atlas, before the runner is constructed: the runner
// reads otel.Tracer at package init, so Init must run first.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/tunedev/atlas/internal/config"
)

// Init builds the trace provider and returns a shutdown func that must run on
// every exit path, including error paths, or a short run's spans are dropped
// with it.
//
// When cfg.OTel.Enabled is false, Init installs nothing and returns a no-op
// shutdown. The global provider stays the SDK's default no-op, so every span
// in the runner costs nothing and the binary runs with no collector.
//
// Resource attributes come from OTEL_RESOURCE_ATTRIBUTES in the environment.
// Nothing here hardcodes service.name, so the same binary is correct wherever
// it runs.
func Init(ctx context.Context, cfg config.Config) (func(context.Context) error, error) {
	if !cfg.OTel.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx, resource.WithTelemetrySDK(), resource.WithFromEnv())
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTel.Endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(cfg.OTel.ExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build exporter: %w", err)
	}

	// AlwaysSample: the SDK would have to sample before a run's outcome is
	// known, and the collector decides after, which is the only order that can
	// implement "keep every error".
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

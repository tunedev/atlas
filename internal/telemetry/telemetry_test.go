package telemetry_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/tunedev/atlas/internal/config"
	"github.com/tunedev/atlas/internal/telemetry"
)

func TestDisabledPathReturnsNoOpShutdown(t *testing.T) {
	cfg := config.Config{
		OTel: config.OTelConfig{
			Enabled: false,
		},
	}
	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Error("Init returned nil shutdown; must return non-nil shutdown even when disabled")
	}
	err = shutdown(context.Background())
	if err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestEnabledPathDoesNotBlockOnUnreachableEndpoint(t *testing.T) {
	// Init installs its provider as the process-wide global; restore whatever
	// was there before this test so it does not leak into the rest of the
	// binary.
	prior := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prior) })

	cfg := config.Config{
		OTel: config.OTelConfig{
			Enabled:       true,
			Endpoint:      "127.0.0.1:9999",
			ExportTimeout: 10 * time.Second,
		},
	}

	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}

	// Shutdown only exercises the export path if there is something to
	// flush: with zero spans recorded, the batch processor returns
	// immediately regardless of whether the exporter's dial blocks, so this
	// test could not previously fail for the reason it names.
	_, span := otel.Tracer("telemetry_test").Start(context.Background(), "probe")
	span.End()

	// The endpoint is unreachable, so Shutdown erroring is expected; the
	// property under test is that it returns at all rather than hanging
	// past the deadline it was given.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = shutdown(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("shutdown took %s against a 500ms deadline; it blocked on the dial instead of returning", elapsed)
	}
}

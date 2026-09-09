package telemetry_test

import (
	"context"
	"testing"
	"time"

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
	cfg := config.Config{
		OTel: config.OTelConfig{
			Enabled:       true,
			Endpoint:      "127.0.0.1:9999",
			ExportTimeout: 10 * time.Second,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	shutdown, err := telemetry.Init(ctx, cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Error("Init returned nil shutdown")
	}
	err = shutdown(context.Background())
	if err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

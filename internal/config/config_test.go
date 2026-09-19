package config_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/config"
)

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.BaseURL == "" {
		t.Error("Model.BaseURL has no default")
	}
	if cfg.Model.Timeout == 0 {
		t.Error("Model.Timeout defaults to zero; every remote call needs a timeout")
	}
	if cfg.Pack.HTTPTimeout == 0 {
		t.Error("Pack.HTTPTimeout defaults to zero")
	}
}

func TestEnvOverridesDefault(t *testing.T) {
	t.Setenv("ATLAS_MODEL_NAME", "from-env")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Name != "from-env" {
		t.Errorf("Model.Name = %q, want from-env", cfg.Model.Name)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_MODEL_NAME", "from-env")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-model-name", "from-flag"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Name != "from-flag" {
		t.Errorf("Model.Name = %q, want from-flag; flags are the last layer", cfg.Model.Name)
	}
}

func TestPackPathIsRequired(t *testing.T) {
	if _, err := config.Load([]string{}); err == nil {
		t.Error("Load succeeded with no pack path; there is nothing to run without one")
	}
}

func TestZeroTimeoutIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-model-timeout", "0s"}); err == nil {
		t.Error("Load accepted a zero model timeout; invalid config must fail at boot")
	}
}

func TestOTelExportTimeoutFlag(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-otel-export-timeout", "5s"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OTel.ExportTimeout.String() != "5s" {
		t.Errorf("OTel.ExportTimeout = %s, want 5s", cfg.OTel.ExportTimeout)
	}
}

func TestZeroOTelExportTimeoutIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-otel-export-timeout", "0s"}); err == nil {
		t.Error("Load accepted a zero otel export timeout; invalid config must fail at boot")
	}
}

func TestMalformedOTelExportTimeoutEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_OTEL_EXPORT_TIMEOUT", "5seconds")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_OTEL_EXPORT_TIMEOUT; invalid env must fail at boot")
	}
}

func TestPackHTTPTimeoutEnvVar(t *testing.T) {
	t.Setenv("ATLAS_PACK_HTTP_TIMEOUT", "7s")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Pack.HTTPTimeout.String() != "7s" {
		t.Errorf("Pack.HTTPTimeout = %s, want 7s", cfg.Pack.HTTPTimeout)
	}
}

func TestMalformedPackHTTPTimeoutEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_PACK_HTTP_TIMEOUT", "notaduration")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_PACK_HTTP_TIMEOUT; invalid env must fail at boot")
	}
}

func TestPackHTTPMaxBytesEnvVar(t *testing.T) {
	t.Setenv("ATLAS_PACK_HTTP_MAX_BYTES", "12345")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Pack.HTTPMaxBytes != 12345 {
		t.Errorf("Pack.HTTPMaxBytes = %d, want 12345", cfg.Pack.HTTPMaxBytes)
	}
}

func TestMalformedPackHTTPMaxBytesEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_PACK_HTTP_MAX_BYTES", "notanumber")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_PACK_HTTP_MAX_BYTES; invalid env must fail at boot")
	}
}

func TestModelTimeoutEnvVar(t *testing.T) {
	t.Setenv("ATLAS_MODEL_TIMEOUT", "9s")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Timeout.String() != "9s" {
		t.Errorf("Model.Timeout = %s, want 9s", cfg.Model.Timeout)
	}
}

func TestMalformedModelTimeoutEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_MODEL_TIMEOUT", "notaduration")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_MODEL_TIMEOUT; invalid env must fail at boot")
	}
}

func TestModelMaxBytesEnvVar(t *testing.T) {
	t.Setenv("ATLAS_MODEL_MAX_BYTES", "54321")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.MaxBytes != 54321 {
		t.Errorf("Model.MaxBytes = %d, want 54321", cfg.Model.MaxBytes)
	}
}

func TestMalformedModelMaxBytesEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_MODEL_MAX_BYTES", "notanumber")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_MODEL_MAX_BYTES; invalid env must fail at boot")
	}
}

func TestOTelEnabledEnvVarTurnsOTelOn(t *testing.T) {
	t.Setenv("ATLAS_OTEL_ENABLED", "true")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OTel.Enabled {
		t.Error("OTel.Enabled = false, want true")
	}
}

func TestOTelEnabledEnvVarTurnsOTelOff(t *testing.T) {
	t.Setenv("ATLAS_OTEL_ENDPOINT", "collector:4317")
	t.Setenv("ATLAS_OTEL_ENABLED", "false")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OTel.Enabled {
		t.Error("OTel.Enabled = true; ATLAS_OTEL_ENABLED=false must be able to turn it back off")
	}
	if cfg.OTel.Endpoint != "collector:4317" {
		t.Errorf("OTel.Endpoint = %q, want collector:4317; the endpoint must still be set", cfg.OTel.Endpoint)
	}
}

func TestOTelEndpointEnvVarAloneDoesNotEnableOTel(t *testing.T) {
	t.Setenv("ATLAS_OTEL_ENDPOINT", "collector:4317")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OTel.Enabled {
		t.Error("OTel.Enabled = true; setting the endpoint must not have the side effect of enabling export")
	}
}

func TestMalformedOTelEnabledEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_OTEL_ENABLED", "notabool")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("Load accepted malformed ATLAS_OTEL_ENABLED; invalid env must fail at boot")
	}
}

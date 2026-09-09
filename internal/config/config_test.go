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

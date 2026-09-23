package config_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestModelAPIKeyDefaultsToEmpty(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.APIKey != "" {
		t.Error("Model.APIKey has a non-empty default; a local engine needs no key")
	}
}

func TestModelAPIKeyEnvVar(t *testing.T) {
	t.Setenv("ATLAS_MODEL_API_KEY", "from-env-key")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.APIKey != "from-env-key" {
		t.Errorf("Model.APIKey = %q, want from-env-key", cfg.Model.APIKey)
	}
}

// TestModelAPIKeyNeverAppearsInUsageOutput proves the key cannot leak through
// flag usage text: flag prints every flag's default value on usage, so a key
// registered as a flag default would appear in ps, shell history, and here.
// The key is env-only and never registered as a flag default.
func TestModelAPIKeyNeverAppearsInUsageOutput(t *testing.T) {
	t.Setenv("ATLAS_MODEL_API_KEY", "sk-SECRET-123")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w

	_, loadErr := config.Load([]string{"-pack", "p.yaml", "-bogus-flag"})

	os.Stderr = orig
	w.Close()
	var out bytes.Buffer
	io.Copy(&out, r)

	if loadErr == nil {
		t.Fatal("Load succeeded with an unknown flag")
	}
	if strings.Contains(out.String(), "sk-SECRET-123") {
		t.Errorf("usage output leaked the API key: %s", out.String())
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

func TestStoreDefaultsExpandLeadingTilde(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if strings.HasPrefix(cfg.Store.Root, "~") {
		t.Errorf("Store.Root = %q; leading ~ was not expanded", cfg.Store.Root)
	}
	wantRoot := filepath.Join(home, ".atlas", "workspace")
	if cfg.Store.Root != wantRoot {
		t.Errorf("Store.Root = %q, want %q", cfg.Store.Root, wantRoot)
	}
	wantIndex := filepath.Join(home, ".atlas", "index.db")
	if cfg.Store.IndexPath != wantIndex {
		t.Errorf("Store.IndexPath = %q, want %q", cfg.Store.IndexPath, wantIndex)
	}
	wantHistory := filepath.Join(home, ".atlas", "history.duckdb")
	if cfg.Store.HistoryPath != wantHistory {
		t.Errorf("Store.HistoryPath = %q, want %q", cfg.Store.HistoryPath, wantHistory)
	}
}

func TestStoreRootEnvVar(t *testing.T) {
	t.Setenv("ATLAS_STORE_ROOT", "/tmp/custom-root")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Store.Root != "/tmp/custom-root" {
		t.Errorf("Store.Root = %q, want /tmp/custom-root", cfg.Store.Root)
	}
}

func TestStoreRootFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_STORE_ROOT", "/tmp/from-env")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-store-root", "/tmp/from-flag"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Store.Root != "/tmp/from-flag" {
		t.Errorf("Store.Root = %q, want /tmp/from-flag; flags are the last layer", cfg.Store.Root)
	}
}

func TestStoreIndexPathEnvVar(t *testing.T) {
	t.Setenv("ATLAS_STORE_INDEX_PATH", "/tmp/custom-index.db")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Store.IndexPath != "/tmp/custom-index.db" {
		t.Errorf("Store.IndexPath = %q, want /tmp/custom-index.db", cfg.Store.IndexPath)
	}
}

func TestStoreHistoryPathEnvVar(t *testing.T) {
	t.Setenv("ATLAS_STORE_HISTORY_PATH", "/tmp/custom-history.duckdb")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Store.HistoryPath != "/tmp/custom-history.duckdb" {
		t.Errorf("Store.HistoryPath = %q, want /tmp/custom-history.duckdb", cfg.Store.HistoryPath)
	}
}

func TestStoreIndexPathTildeExpansion(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-store-index-path", "~/custom-index.db"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, "custom-index.db")
	if cfg.Store.IndexPath != want {
		t.Errorf("Store.IndexPath = %q, want %q", cfg.Store.IndexPath, want)
	}
}

func TestEmptyStoreRootIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-store-root", ""}); err == nil {
		t.Error("Load accepted an empty store root")
	}
}

func TestEmptyStoreIndexPathIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-store-index-path", ""}); err == nil {
		t.Error("Load accepted an empty store index path")
	}
}

func TestEmptyStoreHistoryPathIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-store-history-path", ""}); err == nil {
		t.Error("Load accepted an empty store history path")
	}
}

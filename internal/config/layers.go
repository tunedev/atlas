package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Load resolves configuration from defaults, then environment, then flags.
// A model call on a laptop is slow, so its timeout default is generous where
// the HTTP one is not.
func Load(args []string) (Config, error) {
	cfg := defaults()
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := applyFlags(&cfg, args); err != nil {
		return Config{}, err
	}
	if err := expandStorePaths(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// expandStorePaths resolves a leading "~" in each store path against the
// user's home directory, so the rest of the program never handles a literal
// "~". It fails loudly rather than falling back to a relative path if the
// home directory cannot be determined.
func expandStorePaths(c *Config) error {
	root, err := expandHome(c.Store.Root)
	if err != nil {
		return err
	}
	c.Store.Root = root

	indexPath, err := expandHome(c.Store.IndexPath)
	if err != nil {
		return err
	}
	c.Store.IndexPath = indexPath

	historyPath, err := expandHome(c.Store.HistoryPath)
	if err != nil {
		return err
	}
	c.Store.HistoryPath = historyPath

	return nil
}

// expandHome replaces a leading "~" or "~/" in path with the user's home
// directory. A path with no such prefix is returned unchanged.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func defaults() Config {
	return Config{
		Pack: PackConfig{
			HTTPTimeout:  20 * time.Second,
			HTTPMaxBytes: 10 * 1024 * 1024,
		},
		Model: ModelConfig{
			BaseURL:  "http://localhost:11434/v1",
			Name:     "qwen2.5-coder:7b",
			Timeout:  5 * time.Minute,
			MaxBytes: 10 * 1024 * 1024,
		},
		OTel: OTelConfig{
			Endpoint:      "localhost:4317",
			ExportTimeout: 10 * time.Second,
			Enabled:       false,
		},
		Store: StoreConfig{
			Root:        "~/.atlas/workspace",
			IndexPath:   "~/.atlas/index.db",
			HistoryPath: "~/.atlas/history.duckdb",
		},
	}
}

func applyEnv(c *Config) error {
	if v := os.Getenv("ATLAS_PACK"); v != "" {
		c.Pack.Path = v
	}
	if v := os.Getenv("ATLAS_PACK_HTTP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_PACK_HTTP_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Pack.HTTPTimeout = d
	}
	if v := os.Getenv("ATLAS_PACK_HTTP_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_PACK_HTTP_MAX_BYTES: invalid integer %q: %w", v, err)
		}
		c.Pack.HTTPMaxBytes = n
	}
	if v := os.Getenv("ATLAS_MODEL_BASE_URL"); v != "" {
		c.Model.BaseURL = v
	}
	if v := os.Getenv("ATLAS_MODEL_NAME"); v != "" {
		c.Model.Name = v
	}
	if v := os.Getenv("ATLAS_MODEL_API_KEY"); v != "" {
		c.Model.APIKey = v
	}
	if v := os.Getenv("ATLAS_MODEL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_MODEL_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Model.Timeout = d
	}
	if v := os.Getenv("ATLAS_MODEL_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_MODEL_MAX_BYTES: invalid integer %q: %w", v, err)
		}
		c.Model.MaxBytes = n
	}
	if v := os.Getenv("ATLAS_OTEL_ENDPOINT"); v != "" {
		c.OTel.Endpoint = v
	}
	if v := os.Getenv("ATLAS_OTEL_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_OTEL_ENABLED: invalid bool %q: %w", v, err)
		}
		c.OTel.Enabled = b
	}
	if v := os.Getenv("ATLAS_OTEL_EXPORT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_OTEL_EXPORT_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.OTel.ExportTimeout = d
	}
	if v := os.Getenv("ATLAS_STORE_ROOT"); v != "" {
		c.Store.Root = v
	}
	if v := os.Getenv("ATLAS_STORE_INDEX_PATH"); v != "" {
		c.Store.IndexPath = v
	}
	if v := os.Getenv("ATLAS_STORE_HISTORY_PATH"); v != "" {
		c.Store.HistoryPath = v
	}
	return nil
}

func applyFlags(c *Config, args []string) error {
	fs := flag.NewFlagSet("atlas", flag.ContinueOnError)
	fs.StringVar(&c.Pack.Path, "pack", c.Pack.Path, "path to a pack file")
	fs.DurationVar(&c.Pack.HTTPTimeout, "http-timeout", c.Pack.HTTPTimeout, "timeout for http.request")
	fs.Int64Var(&c.Pack.HTTPMaxBytes, "http-max-bytes", c.Pack.HTTPMaxBytes, "max response body size for http.request, in bytes")
	fs.StringVar(&c.Model.BaseURL, "model-base-url", c.Model.BaseURL, "OpenAI-compatible base URL")
	fs.StringVar(&c.Model.Name, "model-name", c.Model.Name, "model identifier")
	fs.DurationVar(&c.Model.Timeout, "model-timeout", c.Model.Timeout, "model call timeout")
	fs.Int64Var(&c.Model.MaxBytes, "model-max-bytes", c.Model.MaxBytes, "max response body size for model.complete, in bytes")
	fs.BoolVar(&c.OTel.Enabled, "otel", c.OTel.Enabled, "export traces over OTLP")
	fs.DurationVar(&c.OTel.ExportTimeout, "otel-export-timeout", c.OTel.ExportTimeout, "timeout for OTLP span export")
	fs.StringVar(&c.Store.Root, "store-root", c.Store.Root, "root directory of the git-backed document store")
	fs.StringVar(&c.Store.IndexPath, "store-index-path", c.Store.IndexPath, "path to the SQLite index database")
	fs.StringVar(&c.Store.HistoryPath, "store-history-path", c.Store.HistoryPath, "path to the DuckDB history database")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	return nil
}

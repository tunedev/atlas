package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// Load resolves configuration from defaults, then environment, then flags.
// A model call on a laptop is slow, so its timeout default is generous where
// the HTTP one is not.
func Load(args []string) (Config, error) {
	cfg := defaults()
	applyEnv(&cfg)
	if err := applyFlags(&cfg, args); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		Pack: PackConfig{
			HTTPTimeout:  20 * time.Second,
			HTTPMaxBytes: 10 * 1024 * 1024,
		},
		Model: ModelConfig{
			BaseURL:  "http://localhost:11434/v1",
			Name:     "qwen3.5:9b",
			Timeout:  5 * time.Minute,
			MaxBytes: 10 * 1024 * 1024,
		},
		OTel: OTelConfig{
			Endpoint:      "localhost:4317",
			ExportTimeout: 10 * time.Second,
			Enabled:       false,
		},
	}
}

func applyEnv(c *Config) {
	if v := os.Getenv("ATLAS_PACK"); v != "" {
		c.Pack.Path = v
	}
	if v := os.Getenv("ATLAS_MODEL_BASE_URL"); v != "" {
		c.Model.BaseURL = v
	}
	if v := os.Getenv("ATLAS_MODEL_NAME"); v != "" {
		c.Model.Name = v
	}
	if v := os.Getenv("ATLAS_OTEL_ENDPOINT"); v != "" {
		c.OTel.Endpoint = v
		c.OTel.Enabled = true
	}
	if v := os.Getenv("ATLAS_OTEL_EXPORT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.OTel.ExportTimeout = d
		}
	}
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
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	return nil
}

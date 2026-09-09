// Package config loads atlas's configuration in layers: defaults, then
// environment, then flags — later layers win. The result is validated once at
// startup; nothing here is read in a run path.
package config

import (
	"fmt"
	"time"
)

// Config is the fully resolved, validated configuration for the atlas binary.
// Nothing here describes any particular use case: what to run comes from a
// pack file, named by Pack.Path.
type Config struct {
	Pack  PackConfig
	Model ModelConfig
	OTel  OTelConfig
}

type PackConfig struct {
	Path        string
	HTTPTimeout time.Duration
}

type ModelConfig struct {
	BaseURL string
	Name    string
	Timeout time.Duration
}

type OTelConfig struct {
	Endpoint      string
	ExportTimeout time.Duration
	Enabled       bool
}

func (c Config) validate() error {
	if c.Pack.Path == "" {
		return fmt.Errorf("config: no pack path; pass -pack")
	}
	if c.Pack.HTTPTimeout <= 0 {
		return fmt.Errorf("config: http timeout must be positive, got %s", c.Pack.HTTPTimeout)
	}
	if c.Model.Timeout <= 0 {
		return fmt.Errorf("config: model timeout must be positive, got %s", c.Model.Timeout)
	}
	if c.Model.Name == "" {
		return fmt.Errorf("config: model name is empty")
	}
	if c.Model.BaseURL == "" {
		return fmt.Errorf("config: model base URL is empty")
	}
	return nil
}

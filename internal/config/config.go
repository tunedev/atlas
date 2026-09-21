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
	Store StoreConfig
}

type PackConfig struct {
	Path         string
	HTTPTimeout  time.Duration
	HTTPMaxBytes int64
}

// ModelConfig configures the one model provider atlas is wired to. APIKey is
// a secret: it defaults to empty so a local engine needs none, is never
// logged, and never appears in any effective-config output.
type ModelConfig struct {
	BaseURL  string
	Name     string
	APIKey   string
	Timeout  time.Duration
	MaxBytes int64
}

type OTelConfig struct {
	Endpoint      string
	ExportTimeout time.Duration
	Enabled       bool
}

// StoreConfig locates the git-backed record and its two derived indices.
// Root, IndexPath and HistoryPath are expanded from a leading "~" at load
// time, so nothing downstream handles that expansion itself.
type StoreConfig struct {
	Root        string
	IndexPath   string
	HistoryPath string
}

func (c Config) validate() error {
	if c.Pack.Path == "" {
		return fmt.Errorf("config: no pack path; pass -pack")
	}
	if c.Pack.HTTPTimeout <= 0 {
		return fmt.Errorf("config: http timeout must be positive, got %s", c.Pack.HTTPTimeout)
	}
	if c.Pack.HTTPMaxBytes <= 0 {
		return fmt.Errorf("config: http max bytes must be positive, got %d", c.Pack.HTTPMaxBytes)
	}
	if c.Model.Timeout <= 0 {
		return fmt.Errorf("config: model timeout must be positive, got %s", c.Model.Timeout)
	}
	if c.Model.MaxBytes <= 0 {
		return fmt.Errorf("config: model max bytes must be positive, got %d", c.Model.MaxBytes)
	}
	if c.Model.Name == "" {
		return fmt.Errorf("config: model name is empty")
	}
	if c.Model.BaseURL == "" {
		return fmt.Errorf("config: model base URL is empty")
	}
	if c.OTel.ExportTimeout <= 0 {
		return fmt.Errorf("config: otel export timeout must be positive, got %s", c.OTel.ExportTimeout)
	}
	if c.Store.Root == "" {
		return fmt.Errorf("config: store root is empty")
	}
	if c.Store.IndexPath == "" {
		return fmt.Errorf("config: store index path is empty")
	}
	if c.Store.HistoryPath == "" {
		return fmt.Errorf("config: store history path is empty")
	}
	return nil
}

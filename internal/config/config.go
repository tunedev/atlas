// Package config loads atlas's configuration in layers: defaults, then
// environment, then flags — later layers win. The result is validated once at
// startup; nothing here is read in a run path.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Config is the fully resolved, validated configuration for the atlas binary.
// Nothing here describes any particular use case: what to run comes from a
// pack file, named by Pack.Path.
type Config struct {
	Pack       PackConfig
	Model      ModelConfig
	OTel       OTelConfig
	Store      StoreConfig
	Judge      JudgeConfig
	Agent      AgentConfig
	Permission PermissionConfig
}

type PackConfig struct {
	Path         string
	HTTPTimeout  time.Duration
	HTTPMaxBytes int64
}

// ModelConfig configures the one model provider atlas is wired to. APIKey is
// a secret: it comes only from ATLAS_MODEL_API_KEY, has no flag, defaults to
// empty so a local engine needs none, and is never printed in usage, logs,
// or any effective-config output.
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

// JudgeConfig pins how the judge samples, so a judged probability does not
// move between runs. TopLogProbs bounds how many alternatives per token the
// judge reads mass from; MaxTokens bounds the reply.
type JudgeConfig struct {
	Temperature float64
	Seed        int
	TopLogProbs int
	MaxTokens   int
}

// StoreConfig locates the git-backed record and its two derived indices.
// Root, IndexPath and HistoryPath are expanded from a leading "~" at load
// time, so nothing downstream handles that expansion itself.
type StoreConfig struct {
	Root        string
	IndexPath   string
	HistoryPath string
}

// AgentConfig configures the one coding agent atlas can drive. The agent is
// enabled only when Command is set. Tools names the registry tools offered
// to it. WorkDir is the absolute default directory it works in.
type AgentConfig struct {
	Command            string
	Args               []string
	WorkDir            string
	Tools              []string
	MCPAddr            string
	StartTimeout       time.Duration
	TurnTimeout        time.Duration
	CloseTimeout       time.Duration
	MCPHeaderTimeout   time.Duration
	MaxMessageBytes    int
	MaxToolResultBytes int
}

// PermissionConfig holds the rules that decide an agent's tool calls, first
// match wins, and bounds the summary a human is shown.
type PermissionConfig struct {
	Rules        []PermissionRule
	SummaryBytes int
}

// PermissionRule matches a tool name and kind, either of which may be "*".
type PermissionRule struct {
	ToolName string
	Kind     string
	Decision string
}

// AgentEnv is environ without atlas's own variables, so no atlas secret
// reaches the agent process.
func AgentEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "ATLAS_") {
			out = append(out, kv)
		}
	}
	return out
}

// validate checks the agent config, called only when the agent is enabled.
func (a AgentConfig) validate() error {
	if !filepath.IsAbs(a.WorkDir) {
		return fmt.Errorf("config: agent workdir must be absolute, got %q", a.WorkDir)
	}
	for _, t := range []struct {
		name string
		d    time.Duration
	}{
		{"start timeout", a.StartTimeout}, {"turn timeout", a.TurnTimeout},
		{"close timeout", a.CloseTimeout}, {"mcp header timeout", a.MCPHeaderTimeout},
	} {
		if t.d <= 0 {
			return fmt.Errorf("config: agent %s must be positive, got %s", t.name, t.d)
		}
	}
	if a.MaxMessageBytes <= 0 || a.MaxToolResultBytes <= 0 {
		return fmt.Errorf("config: agent max message and tool result bytes must be positive")
	}
	if len(a.Tools) > 0 && a.MCPAddr == "" {
		return fmt.Errorf("config: agent tools are configured but the MCP address is empty")
	}
	return nil
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
	if c.Judge.TopLogProbs < 2 {
		return fmt.Errorf("config: judge top logprobs must be at least 2, got %d", c.Judge.TopLogProbs)
	}
	if c.Judge.MaxTokens <= 0 {
		return fmt.Errorf("config: judge max tokens must be positive, got %d", c.Judge.MaxTokens)
	}
	if c.Judge.Temperature < 0 {
		return fmt.Errorf("config: judge temperature must not be negative, got %v", c.Judge.Temperature)
	}
	if c.Judge.Temperature > 2 {
		return fmt.Errorf("config: judge temperature must not exceed 2, got %v", c.Judge.Temperature)
	}
	for _, r := range c.Permission.Rules {
		switch r.Decision {
		case "allow", "ask", "deny":
		default:
			return fmt.Errorf("config: permission rule %s:%s has unknown decision %q", r.ToolName, r.Kind, r.Decision)
		}
	}
	if c.Permission.SummaryBytes <= 0 {
		return fmt.Errorf("config: permission summary bytes must be positive, got %d", c.Permission.SummaryBytes)
	}
	if c.Agent.Command != "" {
		if err := c.Agent.validate(); err != nil {
			return err
		}
	}
	return nil
}

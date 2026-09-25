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
	if err := expandPaths(&cfg); err != nil {
		return Config{}, err
	}
	if err := resolveWorkDir(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// expandPaths resolves a leading "~" in each store path and the feed cache path
// against the user's home directory, so the rest of the program never handles a literal
// "~". It fails loudly rather than falling back to a relative path if the
// home directory cannot be determined.
func expandPaths(c *Config) error {
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

	cachePath, err := expandHome(c.Feed.CachePath)
	if err != nil {
		return err
	}
	c.Feed.CachePath = cachePath

	crawlCacheDir, err := expandHome(c.Crawl.CacheDir)
	if err != nil {
		return err
	}
	c.Crawl.CacheDir = crawlCacheDir

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

// resolveWorkDir expands a leading "~" in the agent's working directory and,
// if it is still empty, defaults it to the process's current directory.
func resolveWorkDir(c *Config) error {
	workDir, err := expandHome(c.Agent.WorkDir)
	if err != nil {
		return err
	}
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("config: resolve working directory: %w", err)
		}
	}
	c.Agent.WorkDir = workDir
	return nil
}

func defaults() Config {
	return Config{
		Pack: PackConfig{
			HTTPTimeout:  20 * time.Second,
			HTTPMaxBytes: 10 * 1024 * 1024,
			FileMaxBytes: 10 * 1024 * 1024,
			Vars:         map[string]string{},
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
		Feed: FeedConfig{
			RemoteURL:   "https://github.com/tunedev/feed.git",
			Ref:         "refs/heads/main",
			CachePath:   "~/.atlas/feed-cache",
			PullTimeout: 2 * time.Minute,
			StaleAfter:  24 * time.Hour,
		},
		Judge: JudgeConfig{
			Temperature: 0,
			Seed:        1,
			TopLogProbs: 5,
			MaxTokens:   256,
		},
		Agent: AgentConfig{
			MCPAddr:            "127.0.0.1:0",
			StartTimeout:       60 * time.Second,
			TurnTimeout:        30 * time.Minute,
			CloseTimeout:       10 * time.Second,
			MCPHeaderTimeout:   10 * time.Second,
			MaxMessageBytes:    16 * 1024 * 1024,
			MaxToolResultBytes: 1024 * 1024,
		},
		Permission: PermissionConfig{
			SummaryBytes: 200,
		},
		Extract: ExtractConfig{
			Temperature: 0,
			MaxTokens:   4096,
		},
		Crawl: CrawlConfig{
			UserAgent:     "atlas-crawler/0.1 (+https://github.com/tunedev/atlas)",
			Delay:         2 * time.Second,
			Timeout:       20 * time.Second,
			PullTimeout:   10 * time.Minute,
			MaxBytes:      5 * 1024 * 1024,
			Retries:       2,
			CacheDir:      "~/.atlas/crawl-cache",
			Render:        false,
			RenderTimeout: 30 * time.Second,
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
	if v := os.Getenv("ATLAS_PACK_FILE_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_PACK_FILE_MAX_BYTES: invalid integer %q: %w", v, err)
		}
		c.Pack.FileMaxBytes = n
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
	if v := os.Getenv("ATLAS_JUDGE_TEMPERATURE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_JUDGE_TEMPERATURE: invalid float %q: %w", v, err)
		}
		c.Judge.Temperature = f
	}
	if v := os.Getenv("ATLAS_JUDGE_SEED"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_JUDGE_SEED: invalid integer %q: %w", v, err)
		}
		c.Judge.Seed = n
	}
	if v := os.Getenv("ATLAS_JUDGE_TOP_LOGPROBS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_JUDGE_TOP_LOGPROBS: invalid integer %q: %w", v, err)
		}
		c.Judge.TopLogProbs = n
	}
	if v := os.Getenv("ATLAS_JUDGE_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_JUDGE_MAX_TOKENS: invalid integer %q: %w", v, err)
		}
		c.Judge.MaxTokens = n
	}
	if v := os.Getenv("ATLAS_EXTRACT_TEMPERATURE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_EXTRACT_TEMPERATURE: invalid float %q: %w", v, err)
		}
		c.Extract.Temperature = f
	}
	if v := os.Getenv("ATLAS_EXTRACT_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_EXTRACT_MAX_TOKENS: invalid integer %q: %w", v, err)
		}
		c.Extract.MaxTokens = n
	}
	if v := os.Getenv("ATLAS_FEED_REMOTE_URL"); v != "" {
		c.Feed.RemoteURL = v
	}
	if v := os.Getenv("ATLAS_FEED_REF"); v != "" {
		c.Feed.Ref = v
	}
	if v := os.Getenv("ATLAS_FEED_CACHE_PATH"); v != "" {
		c.Feed.CachePath = v
	}
	if v := os.Getenv("ATLAS_FEED_PULL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_FEED_PULL_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Feed.PullTimeout = d
	}
	if v := os.Getenv("ATLAS_FEED_STALE_AFTER"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_FEED_STALE_AFTER: invalid duration %q: %w", v, err)
		}
		c.Feed.StaleAfter = d
	}
	if v := os.Getenv("ATLAS_CRAWL_USER_AGENT"); v != "" {
		c.Crawl.UserAgent = v
	}
	if v := os.Getenv("ATLAS_CRAWL_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_DELAY: invalid duration %q: %w", v, err)
		}
		c.Crawl.Delay = d
	}
	if v := os.Getenv("ATLAS_CRAWL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Crawl.Timeout = d
	}
	if v := os.Getenv("ATLAS_CRAWL_PULL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_PULL_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Crawl.PullTimeout = d
	}
	if v := os.Getenv("ATLAS_CRAWL_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_MAX_BYTES: invalid integer %q: %w", v, err)
		}
		c.Crawl.MaxBytes = n
	}
	if v := os.Getenv("ATLAS_CRAWL_RETRIES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_RETRIES: invalid integer %q: %w", v, err)
		}
		c.Crawl.Retries = n
	}
	if v := os.Getenv("ATLAS_CRAWL_CACHE_DIR"); v != "" {
		c.Crawl.CacheDir = v
	}
	if v := os.Getenv("ATLAS_CRAWL_RENDER"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_RENDER: invalid bool %q: %w", v, err)
		}
		c.Crawl.Render = b
	}
	if v := os.Getenv("ATLAS_CRAWL_RENDER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_CRAWL_RENDER_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Crawl.RenderTimeout = d
	}
	if v := os.Getenv("ATLAS_AGENT_COMMAND"); v != "" {
		c.Agent.Command = v
	}
	if v := os.Getenv("ATLAS_AGENT_ARGS"); v != "" {
		c.Agent.Args = strings.Fields(v)
	}
	if v := os.Getenv("ATLAS_AGENT_WORKDIR"); v != "" {
		c.Agent.WorkDir = v
	}
	if v := os.Getenv("ATLAS_AGENT_TOOLS"); v != "" {
		c.Agent.Tools = splitList(v)
	}
	if v := os.Getenv("ATLAS_AGENT_MCP_ADDR"); v != "" {
		c.Agent.MCPAddr = strings.TrimSpace(v)
	}
	if v := os.Getenv("ATLAS_AGENT_START_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_START_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Agent.StartTimeout = d
	}
	if v := os.Getenv("ATLAS_AGENT_TURN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_TURN_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Agent.TurnTimeout = d
	}
	if v := os.Getenv("ATLAS_AGENT_CLOSE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_CLOSE_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Agent.CloseTimeout = d
	}
	if v := os.Getenv("ATLAS_AGENT_MCP_HEADER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_MCP_HEADER_TIMEOUT: invalid duration %q: %w", v, err)
		}
		c.Agent.MCPHeaderTimeout = d
	}
	if v := os.Getenv("ATLAS_AGENT_MAX_MESSAGE_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_MAX_MESSAGE_BYTES: invalid integer %q: %w", v, err)
		}
		c.Agent.MaxMessageBytes = n
	}
	if v := os.Getenv("ATLAS_AGENT_MAX_TOOL_RESULT_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_AGENT_MAX_TOOL_RESULT_BYTES: invalid integer %q: %w", v, err)
		}
		c.Agent.MaxToolResultBytes = n
	}
	if v := os.Getenv("ATLAS_PERMISSION_RULES"); v != "" {
		rules, err := parseRules(v)
		if err != nil {
			return err
		}
		c.Permission.Rules = rules
	}
	if v := os.Getenv("ATLAS_PERMISSION_SUMMARY_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: ATLAS_PERMISSION_SUMMARY_BYTES: invalid integer %q: %w", v, err)
		}
		c.Permission.SummaryBytes = n
	}
	return nil
}

// varsFlag collects repeated -var name=value flags into a map. The value is
// everything after the first "=", so a value may itself contain "=".
type varsFlag map[string]string

func (v varsFlag) String() string { return "" }

func (v varsFlag) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return fmt.Errorf("-var %q: want name=value", s)
	}
	v[name] = value
	return nil
}

func applyFlags(c *Config, args []string) error {
	fs := flag.NewFlagSet("atlas", flag.ContinueOnError)
	fs.StringVar(&c.Pack.Path, "pack", c.Pack.Path, "path to a pack file")
	fs.Var(varsFlag(c.Pack.Vars), "var", "override a pack var for this run, as name=value (repeatable)")
	fs.DurationVar(&c.Pack.HTTPTimeout, "http-timeout", c.Pack.HTTPTimeout, "timeout for http.request")
	fs.Int64Var(&c.Pack.HTTPMaxBytes, "http-max-bytes", c.Pack.HTTPMaxBytes, "max response body size for http.request, in bytes")
	fs.Int64Var(&c.Pack.FileMaxBytes, "file-max-bytes", c.Pack.FileMaxBytes, "max size of a file read by file.read or file.text, in bytes")
	fs.StringVar(&c.Model.BaseURL, "model-base-url", c.Model.BaseURL, "OpenAI-compatible base URL")
	fs.StringVar(&c.Model.Name, "model-name", c.Model.Name, "model identifier")
	fs.DurationVar(&c.Model.Timeout, "model-timeout", c.Model.Timeout, "model call timeout")
	fs.Int64Var(&c.Model.MaxBytes, "model-max-bytes", c.Model.MaxBytes, "max response body size for model.complete, in bytes")
	fs.BoolVar(&c.OTel.Enabled, "otel", c.OTel.Enabled, "export traces over OTLP")
	fs.DurationVar(&c.OTel.ExportTimeout, "otel-export-timeout", c.OTel.ExportTimeout, "timeout for OTLP span export")
	fs.StringVar(&c.Store.Root, "store-root", c.Store.Root, "root directory of the git-backed document store")
	fs.StringVar(&c.Store.IndexPath, "store-index-path", c.Store.IndexPath, "path to the SQLite index database")
	fs.StringVar(&c.Store.HistoryPath, "store-history-path", c.Store.HistoryPath, "path to the DuckDB history database")
	fs.Float64Var(&c.Judge.Temperature, "judge-temperature", c.Judge.Temperature, "sampling temperature for judge calls")
	fs.IntVar(&c.Judge.Seed, "judge-seed", c.Judge.Seed, "sampling seed for judge calls")
	fs.IntVar(&c.Judge.TopLogProbs, "judge-top-logprobs", c.Judge.TopLogProbs, "alternatives per token the judge reads mass from")
	fs.IntVar(&c.Judge.MaxTokens, "judge-max-tokens", c.Judge.MaxTokens, "max reply tokens for judge calls")
	fs.Float64Var(&c.Extract.Temperature, "extract-temperature", c.Extract.Temperature, "sampling temperature for extraction")
	fs.IntVar(&c.Extract.MaxTokens, "extract-max-tokens", c.Extract.MaxTokens, "max reply tokens for extraction")
	fs.StringVar(&c.Feed.RemoteURL, "feed-remote-url", c.Feed.RemoteURL, "git remote of the public feed")
	fs.StringVar(&c.Feed.Ref, "feed-ref", c.Feed.Ref, "full ref of the feed to follow: refs/heads/... or refs/tags/...")
	fs.StringVar(&c.Feed.CachePath, "feed-cache-path", c.Feed.CachePath, "directory of the feed's local cache; never inside the store root")
	fs.DurationVar(&c.Feed.PullTimeout, "feed-pull-timeout", c.Feed.PullTimeout, "timeout for one feed pull")
	fs.DurationVar(&c.Feed.StaleAfter, "feed-stale-after", c.Feed.StaleAfter, "feed age past which source.pull warns")
	fs.StringVar(&c.Crawl.UserAgent, "crawl-user-agent", c.Crawl.UserAgent, "crawler user agent; must name a contact URL or email")
	fs.DurationVar(&c.Crawl.Delay, "crawl-delay", c.Crawl.Delay, "least gap between two requests to one host (at least 1s)")
	fs.DurationVar(&c.Crawl.Timeout, "crawl-timeout", c.Crawl.Timeout, "timeout for one crawl request")
	fs.DurationVar(&c.Crawl.PullTimeout, "crawl-pull-timeout", c.Crawl.PullTimeout, "timeout for one crawl pull")
	fs.Int64Var(&c.Crawl.MaxBytes, "crawl-max-bytes", c.Crawl.MaxBytes, "max response body size for a crawl request, in bytes")
	fs.IntVar(&c.Crawl.Retries, "crawl-retries", c.Crawl.Retries, "max retries for a crawl request")
	fs.StringVar(&c.Crawl.CacheDir, "crawl-cache-dir", c.Crawl.CacheDir, "directory of the crawler's local cache; never inside the store root")
	fs.BoolVar(&c.Crawl.Render, "crawl-render", c.Crawl.Render, "render JavaScript pages with a browser already on this machine; never downloads one")
	fs.DurationVar(&c.Crawl.RenderTimeout, "crawl-render-timeout", c.Crawl.RenderTimeout, "timeout for one render")
	fs.StringVar(&c.Agent.Command, "agent-command", c.Agent.Command, "command that starts the coding agent; empty disables it")
	fs.Func("agent-args", "space-separated arguments for the agent command", func(v string) error {
		c.Agent.Args = strings.Fields(v)
		return nil
	})
	fs.StringVar(&c.Agent.WorkDir, "agent-workdir", c.Agent.WorkDir, "absolute default working directory for the agent")
	fs.Func("agent-tools", "comma-separated registry tools offered to the agent", func(v string) error {
		c.Agent.Tools = splitList(v)
		return nil
	})
	fs.DurationVar(&c.Agent.TurnTimeout, "agent-turn-timeout", c.Agent.TurnTimeout, "timeout for one agent turn")
	fs.Func("permission-rules", "comma-separated tool:kind:decision permission rules, first match wins", func(v string) error {
		rules, err := parseRules(v)
		if err != nil {
			return err
		}
		c.Permission.Rules = rules
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	return nil
}

// splitList splits a comma-separated list, trimming space and dropping
// empty entries.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseRules reads "tool:kind:decision" entries from a comma-separated list.
func parseRules(s string) ([]PermissionRule, error) {
	var rules []PermissionRule
	for _, entry := range splitList(s) {
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("config: permission rule %q is not tool:kind:decision", entry)
		}
		rules = append(rules, PermissionRule{ToolName: parts[0], Kind: parts[1], Decision: parts[2]})
	}
	return rules, nil
}

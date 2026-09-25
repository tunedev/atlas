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
	Pack    PackConfig
	Model   ModelConfig
	OTel    OTelConfig
	Store   StoreConfig
	Feed    FeedConfig
	Judge   JudgeConfig
	Extract ExtractConfig
	Crawl   CrawlConfig
}

type PackConfig struct {
	Path         string
	HTTPTimeout  time.Duration
	HTTPMaxBytes int64
	// FileMaxBytes bounds a file.read or file.text call. Over-limit fails; it
	// is never truncated.
	FileMaxBytes int64
	// Vars overrides the pack's own vars for one run. Filled from repeated
	// -var name=value flags only: vars are per-run input, not configuration.
	Vars map[string]string
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

// ExtractConfig pins how extraction samples. Temperature 0 means the same
// text gives the same value; MaxTokens bounds the reply, which for a long
// document is far larger than a judgement's.
type ExtractConfig struct {
	Temperature float64
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

// FeedConfig locates the public feed atlas pulls as a Source and says how
// old it may be before it is reported stale. Ref is a full ref name under
// refs/heads/ or refs/tags/. CachePath is expanded from a leading "~" and
// may not overlap Store.Root, so pulling public data can never touch the
// private record.
type FeedConfig struct {
	RemoteURL   string
	Ref         string
	CachePath   string
	PullTimeout time.Duration
	StaleAfter  time.Duration
}

// CrawlConfig governs the local crawler. The user agent must name a contact
// (a URL or an email address), and Delay, the least gap between two requests
// to one host, cannot go below minCrawlDelay. robots.txt is always honoured
// and has no setting.
type CrawlConfig struct {
	UserAgent     string
	Delay         time.Duration
	Timeout       time.Duration
	PullTimeout   time.Duration
	MaxBytes      int64
	Retries       int
	CacheDir      string
	Render        bool
	RenderTimeout time.Duration
}

// minCrawlDelay is the least gap between two requests to one host that the
// crawler may be configured with.
const minCrawlDelay = time.Second

// identified reports whether a user agent names a contact.
func identified(ua string) bool {
	return strings.Contains(ua, "http://") || strings.Contains(ua, "https://") || strings.Contains(ua, "@")
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
	if c.Pack.FileMaxBytes <= 0 {
		return fmt.Errorf("config: file max bytes must be positive, got %d", c.Pack.FileMaxBytes)
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
	if c.Extract.MaxTokens <= 0 {
		return fmt.Errorf("config: extract max tokens must be positive, got %d", c.Extract.MaxTokens)
	}
	if c.Extract.Temperature < 0 || c.Extract.Temperature > 2 {
		return fmt.Errorf("config: extract temperature must be within [0, 2], got %v", c.Extract.Temperature)
	}
	if c.Feed.RemoteURL == "" {
		return fmt.Errorf("config: feed remote URL is empty")
	}
	if !strings.HasPrefix(c.Feed.Ref, "refs/heads/") && !strings.HasPrefix(c.Feed.Ref, "refs/tags/") {
		return fmt.Errorf("config: feed ref must be a full ref name under refs/heads/ or refs/tags/, got %q", c.Feed.Ref)
	}
	if c.Feed.CachePath == "" {
		return fmt.Errorf("config: feed cache path is empty")
	}
	if overlaps(c.Feed.CachePath, c.Store.Root) {
		return fmt.Errorf("config: feed cache path %s overlaps store root %s; the public feed must never share a directory with the private record", c.Feed.CachePath, c.Store.Root)
	}
	if c.Feed.PullTimeout <= 0 {
		return fmt.Errorf("config: feed pull timeout must be positive, got %s", c.Feed.PullTimeout)
	}
	if c.Feed.StaleAfter <= 0 {
		return fmt.Errorf("config: feed stale-after must be positive, got %s", c.Feed.StaleAfter)
	}
	if !identified(c.Crawl.UserAgent) {
		return fmt.Errorf("config: crawl user agent %q must identify the crawler with a contact URL or email", c.Crawl.UserAgent)
	}
	if c.Crawl.Delay < minCrawlDelay {
		return fmt.Errorf("config: crawl delay must be at least %s, got %s", minCrawlDelay, c.Crawl.Delay)
	}
	if c.Crawl.Timeout <= 0 {
		return fmt.Errorf("config: crawl timeout must be positive, got %s", c.Crawl.Timeout)
	}
	if c.Crawl.PullTimeout <= 0 {
		return fmt.Errorf("config: crawl pull timeout must be positive, got %s", c.Crawl.PullTimeout)
	}
	if c.Crawl.MaxBytes <= 0 {
		return fmt.Errorf("config: crawl max bytes must be positive, got %d", c.Crawl.MaxBytes)
	}
	if c.Crawl.Retries < 0 {
		return fmt.Errorf("config: crawl retries must not be negative, got %d", c.Crawl.Retries)
	}
	if c.Crawl.RenderTimeout <= 0 {
		return fmt.Errorf("config: crawl render timeout must be positive, got %s", c.Crawl.RenderTimeout)
	}
	if c.Crawl.CacheDir == "" {
		return fmt.Errorf("config: crawl cache dir is empty")
	}
	if overlaps(c.Crawl.CacheDir, c.Store.Root) {
		return fmt.Errorf("config: crawl cache dir %s overlaps store root %s; fetched pages must never share a directory with the private record", c.Crawl.CacheDir, c.Store.Root)
	}
	return nil
}

// overlaps reports whether either path is the other or lies beneath it.
func overlaps(a, b string) bool {
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return within(absA, absB) || within(absB, absA)
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

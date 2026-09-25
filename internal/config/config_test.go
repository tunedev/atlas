package config_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestJudgeDefaultsArePinnedForReproducibility(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.Temperature != 0 {
		t.Errorf("Judge.Temperature = %v, want 0; a judged probability must not move between runs", cfg.Judge.Temperature)
	}
	if cfg.Judge.TopLogProbs < 2 {
		t.Errorf("Judge.TopLogProbs = %d, want at least 2; without alternatives there is no mass to sum", cfg.Judge.TopLogProbs)
	}
	if cfg.Judge.MaxTokens <= 0 {
		t.Errorf("Judge.MaxTokens = %d, want a positive default", cfg.Judge.MaxTokens)
	}
}

func TestJudgeTopLogProbsEnvVar(t *testing.T) {
	t.Setenv("ATLAS_JUDGE_TOP_LOGPROBS", "9")
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.TopLogProbs != 9 {
		t.Errorf("Judge.TopLogProbs = %d, want 9", cfg.Judge.TopLogProbs)
	}
}

func TestJudgeSeedFlagOverridesEnv(t *testing.T) {
	t.Setenv("ATLAS_JUDGE_SEED", "3")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-judge-seed", "11"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Judge.Seed != 11 {
		t.Errorf("Judge.Seed = %d, want 11; flags are the last layer", cfg.Judge.Seed)
	}
}

func TestAnInvalidJudgeTopLogProbsIsRejectedAtStartup(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-judge-top-logprobs", "0"}); err == nil {
		t.Fatal("zero top logprobs was accepted; the judge would have no alternatives to sum")
	}
}

func TestATooHighJudgeTemperatureIsRejectedAtStartup(t *testing.T) {
	_, err := config.Load([]string{"-pack", "p.yaml", "-judge-temperature", "12"})
	if err == nil {
		t.Fatal("a judge temperature of 12 was accepted; temperature has an upper bound")
	}
	if !strings.Contains(err.Error(), "judge temperature") {
		t.Errorf("error does not name the setting: %v", err)
	}
}

func TestVarFlagsCollectNameValuePairs(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-var", "port=Calais", "-var", "note=a=b"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Pack.Vars["port"] != "Calais" || cfg.Pack.Vars["note"] != "a=b" {
		t.Errorf("vars = %v", cfg.Pack.Vars)
	}
}

func TestAVarFlagWithoutEqualsIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-var", "port"}); err == nil {
		t.Fatal("-var port was accepted with no value")
	}
}

func TestFileMaxBytesHasAPositiveDefaultAndAnEnvVar(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil || cfg.Pack.FileMaxBytes <= 0 {
		t.Fatalf("default FileMaxBytes = %d, err = %v", cfg.Pack.FileMaxBytes, err)
	}
	t.Setenv("ATLAS_PACK_FILE_MAX_BYTES", "2048")
	cfg, err = config.Load([]string{"-pack", "p.yaml"})
	if err != nil || cfg.Pack.FileMaxBytes != 2048 {
		t.Errorf("FileMaxBytes = %d, err = %v", cfg.Pack.FileMaxBytes, err)
	}
}

func TestZeroFileMaxBytesIsRejected(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-file-max-bytes", "0"}); err == nil {
		t.Fatal("a zero file size limit was accepted")
	}
}

func TestExtractConfigDefaultsAndValidation(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil || cfg.Extract.MaxTokens <= 0 || cfg.Extract.Temperature != 0 {
		t.Fatalf("extract = %+v, err = %v", cfg.Extract, err)
	}
	t.Setenv("ATLAS_EXTRACT_MAX_TOKENS", "8192")
	cfg, err = config.Load([]string{"-pack", "p.yaml", "-extract-temperature", "0.2"})
	if err != nil || cfg.Extract.MaxTokens != 8192 || cfg.Extract.Temperature != 0.2 {
		t.Errorf("extract = %+v, err = %v", cfg.Extract, err)
	}
	if _, err := config.Load([]string{"-pack", "p.yaml", "-extract-max-tokens", "0"}); err == nil {
		t.Error("zero extract max tokens was accepted")
	}
	if _, err := config.Load([]string{"-pack", "p.yaml", "-extract-temperature", "3"}); err == nil {
		t.Error("an extract temperature above 2 was accepted")
	}
}

func TestFeedDefaults(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f := cfg.Feed
	if f.RemoteURL == "" || f.Ref != "refs/heads/main" || f.PullTimeout <= 0 || f.StaleAfter != 24*time.Hour {
		t.Errorf("Feed defaults = %+v", f)
	}
	if strings.HasPrefix(f.CachePath, "~") {
		t.Errorf("CachePath %q was not expanded", f.CachePath)
	}
}

func TestFeedLayers(t *testing.T) {
	t.Setenv("ATLAS_FEED_REF", "refs/tags/pre-v2")
	t.Setenv("ATLAS_FEED_STALE_AFTER", "12h")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-feed-stale-after", "6h"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Feed.Ref != "refs/tags/pre-v2" || cfg.Feed.StaleAfter != 6*time.Hour {
		t.Errorf("Feed = %+v; env sets the ref, the flag wins on stale-after", cfg.Feed)
	}
}

func TestFeedCacheOverlappingTheStoreRootIsRejected(t *testing.T) {
	root := t.TempDir()
	for name, cache := range map[string]string{
		"same":   root,
		"inside": filepath.Join(root, "feed"),
		"around": filepath.Dir(root),
	} {
		_, err := config.Load([]string{"-pack", "p.yaml", "-store-root", root, "-feed-cache-path", cache})
		if err == nil {
			t.Errorf("%s: a feed cache overlapping the store root was accepted", name)
		}
	}
	if _, err := config.Load([]string{"-pack", "p.yaml", "-store-root", root, "-feed-cache-path", t.TempDir()}); err != nil {
		t.Errorf("separate directories rejected: %v", err)
	}
}

func TestInvalidFeedConfigIsRejectedAtStartup(t *testing.T) {
	for name, args := range map[string][]string{
		"short ref":         {"-feed-ref", "main"},
		"empty remote":      {"-feed-remote-url", ""},
		"zero stale-after":  {"-feed-stale-after", "0s"},
		"zero pull timeout": {"-feed-pull-timeout", "0s"},
	} {
		if _, err := config.Load(append([]string{"-pack", "p.yaml"}, args...)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMalformedFeedDurationEnvVarIsRejected(t *testing.T) {
	t.Setenv("ATLAS_FEED_STALE_AFTER", "soon")
	if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
		t.Error("a malformed ATLAS_FEED_STALE_AFTER was accepted")
	}
}

func TestCrawlDefaultsAreOffAndPolite(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Crawl
	if c.Render {
		t.Error("rendering is on by default; it must be opt-in")
	}
	if c.Delay < time.Second || c.UserAgent == "" || c.Timeout <= 0 || c.PullTimeout <= 0 || c.MaxBytes <= 0 || c.RenderTimeout <= 0 {
		t.Errorf("crawl defaults = %+v", c)
	}
	if strings.HasPrefix(c.CacheDir, "~") {
		t.Errorf("CacheDir %q was not expanded", c.CacheDir)
	}
}

func TestCrawlLayers(t *testing.T) {
	t.Setenv("ATLAS_CRAWL_DELAY", "3s")
	t.Setenv("ATLAS_CRAWL_RENDER", "true")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-crawl-delay", "4s"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Crawl.Delay != 4*time.Second || !cfg.Crawl.Render {
		t.Errorf("crawl = %+v; env turns rendering on, the flag wins on delay", cfg.Crawl)
	}
}

func TestImpoliteCrawlConfigIsRejectedAtStartup(t *testing.T) {
	for name, args := range map[string][]string{
		"delay under a second": {"-crawl-delay", "500ms"},
		"anonymous user agent": {"-crawl-user-agent", "atlas-crawler/0.1"},
		"empty user agent":     {"-crawl-user-agent", ""},
		"zero timeout":         {"-crawl-timeout", "0s"},
		"zero pull timeout":    {"-crawl-pull-timeout", "0s"},
		"zero max bytes":       {"-crawl-max-bytes", "0"},
		"negative retries":     {"-crawl-retries", "-1"},
		"zero render timeout":  {"-crawl-render-timeout", "0s"},
		"empty cache dir":      {"-crawl-cache-dir", ""},
	} {
		if _, err := config.Load(append([]string{"-pack", "p.yaml"}, args...)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAnIdentifiedUserAgentMayUseAnEmail(t *testing.T) {
	if _, err := config.Load([]string{"-pack", "p.yaml", "-crawl-user-agent", "atlas-crawler/0.1 (me@example.org)"}); err != nil {
		t.Errorf("a user agent with an email contact was rejected: %v", err)
	}
}

func TestCrawlCacheOverlappingTheStoreRootIsRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := config.Load([]string{"-pack", "p.yaml", "-store-root", root, "-crawl-cache-dir", filepath.Join(root, "crawl")}); err == nil {
		t.Error("a crawl cache inside the private record was accepted")
	}
}

func TestAgentIsOffWithoutACommand(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Command != "" {
		t.Errorf("agent command %q; want none by default", cfg.Agent.Command)
	}
	wd, _ := os.Getwd()
	if cfg.Agent.WorkDir != wd {
		t.Errorf("workdir %q; want the process's %q", cfg.Agent.WorkDir, wd)
	}
}

func TestAgentConfigFromEnvAndFlags(t *testing.T) {
	t.Setenv("ATLAS_AGENT_COMMAND", "agent-bin")
	t.Setenv("ATLAS_AGENT_ARGS", "--acp  --quiet")
	t.Setenv("ATLAS_AGENT_TOOLS", "http.request, judge.ask")
	t.Setenv("ATLAS_PERMISSION_RULES", "http.request:atlas:allow, *:execute:ask,*:*:deny")
	workDir := t.TempDir()
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-agent-turn-timeout", "2m", "-agent-workdir", workDir})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Agent.Args, []string{"--acp", "--quiet"}) ||
		!reflect.DeepEqual(cfg.Agent.Tools, []string{"http.request", "judge.ask"}) ||
		cfg.Agent.TurnTimeout != 2*time.Minute || cfg.Agent.WorkDir != workDir {
		t.Errorf("agent config %+v", cfg.Agent)
	}
	want := []config.PermissionRule{
		{ToolName: "http.request", Kind: "atlas", Decision: "allow"},
		{ToolName: "*", Kind: "execute", Decision: "ask"},
		{ToolName: "*", Kind: "*", Decision: "deny"},
	}
	if !reflect.DeepEqual(cfg.Permission.Rules, want) {
		t.Errorf("rules %+v", cfg.Permission.Rules)
	}
}

func TestBadAgentConfigFailsAtLoad(t *testing.T) {
	cases := map[string]map[string]string{
		"bad decision":      {"ATLAS_PERMISSION_RULES": "*:*:maybe"},
		"short rule":        {"ATLAS_PERMISSION_RULES": "*:allow"},
		"relative workdir":  {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_WORKDIR": "work"},
		"zero turn timeout": {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_TURN_TIMEOUT": "0s"},
		"tools, no addr":    {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_TOOLS": "x", "ATLAS_AGENT_MCP_ADDR": " "},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
				t.Error("loaded")
			}
		})
	}
}

func TestAgentEnvDropsAtlasVariables(t *testing.T) {
	got := config.AgentEnv([]string{"HOME=/home/u", "ATLAS_MODEL_API_KEY=secret", "PATH=/bin", "ATLAS_PACK=p"})
	if !reflect.DeepEqual(got, []string{"HOME=/home/u", "PATH=/bin"}) {
		t.Errorf("env %v", got)
	}
}

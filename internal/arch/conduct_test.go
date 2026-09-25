package arch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
	"github.com/tunedev/atlas/internal/config"
)

const crawlPkg = "github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"

// credentialField matches a field name that could only exist to hold a
// credential or send one.
var credentialField = regexp.MustCompile(`(?i)auth|token|secret|passw|cookie|header|credential|login|session|apikey`)

// The crawler holds no credential: no configuration or target type it reads
// has anywhere to put one.
func TestTheCrawlerHasNowhereToHoldACredential(t *testing.T) {
	for _, v := range []any{crawlsource.Config{}, crawlsource.Target{}, crawlsource.Field{}, config.CrawlConfig{}} {
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			if name := typ.Field(i).Name; credentialField.MatchString(name) {
				t.Errorf("%s.%s could hold a credential", typ.Name(), name)
			}
		}
	}
}

// The crawler keeps no cookie jar of its own.
func TestTheCrawlerImportsNoCookieJar(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, crawlPkg).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, imp := range strings.Split(string(out), "\n") {
		if imp == "net/http/cookiejar" {
			t.Error("crawlsource imports net/http/cookiejar")
		}
	}
}

// The crawler never downloads a browser: Rod downloads one when a launcher
// has no Bin, or when a browser connects without a control URL, so every
// launcher must be given Bin and nothing may call the download paths.
func TestTheCrawlerNeverDownloadsABrowser(t *testing.T) {
	dir := filepath.Join("..", "adapters", "outbound", "crawlsource")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, banned := range []string{"NewBrowser(", ".Revision(", "MustLaunch(", "rod.New().Connect(", "rod.New().MustConnect("} {
			if strings.Contains(src, banned) {
				t.Errorf("%s calls %s, which can download a browser", f, banned)
			}
		}
		if n, withBin := strings.Count(src, "launcher.New()"), strings.Count(src, "launcher.New().Bin("); n != withBin {
			t.Errorf("%s: %d launchers, %d given Bin; a launcher without Bin downloads a browser", f, n, withBin)
		}
	}
}

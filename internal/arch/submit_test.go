package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Atlas drafts and never submits. No tool, and nothing a tool fetches
// through, may send a method that writes to a remote host.
func TestNoToolSendsAWriteMethod(t *testing.T) {
	write := regexp.MustCompile(`Method(Post|Put|Patch|Delete)\b|"(POST|PUT|PATCH|DELETE)"`)
	for _, dir := range []string{"../adapters/outbound/tools", "../adapters/outbound/crawlsource"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no files in %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if loc := write.Find(b); loc != nil {
				t.Errorf("%s names a write method (%s); atlas never submits", f, loc)
			}
		}
	}
}

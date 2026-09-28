package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeMethodPattern matches source that names a write HTTP method, in any
// of its forms: the http.MethodX constants, a quoted method string in any
// case, or a call to a Post/PostForm/Put/Patch/Delete convenience method on
// http or an *http.Client. The quoted-method branch requires the quotes to
// directly enclose the word, so an ordinary word like "postpone" never
// matches.
var writeMethodPattern = regexp.MustCompile(
	`Method(Post|Put|Patch|Delete)\b` +
		`|\.(Post|PostForm|Put|Patch|Delete)\(` +
		`|(?i)"(post|put|patch|delete)"`,
)

// Atlas drafts and never submits. No tool, and nothing a tool fetches
// through, may send a method that writes to a remote host.
func TestNoToolSendsAWriteMethod(t *testing.T) {
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
			if loc := writeMethodPattern.Find(b); loc != nil {
				t.Errorf("%s names a write method (%s); atlas never submits", f, loc)
			}
		}
	}
}

// TestTheWriteMethodPatternCatchesEveryForm proves writeMethodPattern matches
// every form a write method can take in source, and does not fire on
// ordinary GET/HEAD code or on words that merely contain a write verb.
func TestTheWriteMethodPatternCatchesEveryForm(t *testing.T) {
	matches := []string{
		`http.MethodPost`,
		`"POST"`,
		`"post"`,
		`http.Post(u, "x", b)`,
		`http.PostForm(u, v)`,
		`c.PostForm(u, v)`,
		`http.NewRequest("delete", u, nil)`,
	}
	for _, s := range matches {
		if !writeMethodPattern.MatchString(s) {
			t.Errorf("%q: want a match, got none", s)
		}
	}

	noMatches := []string{
		`http.MethodGet`,
		`"GET"`,
		`http.Get(u)`,
		`c.Head(u)`,
		`// posted`,
		`// postpone the retry`,
		`// signpost`,
	}
	for _, s := range noMatches {
		if writeMethodPattern.MatchString(s) {
			t.Errorf("%q: want no match, got one", s)
		}
	}
}

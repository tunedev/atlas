package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// {@html} bypasses Svelte's escaping and renders a string as markup. This
// harness renders pack-supplied strings; nothing in web/src may render one
// unescaped.
func TestNoRawHTMLInWebSource(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".svelte" {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), "{@html") {
			t.Errorf("%s uses {@html}; render escaped text instead", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness knows nothing about any use case. A pack supplies every word
// specific to what it does; the Go tree supplies none of them.
//
// This is the difference between a harness and an application with a config
// file, and it is the one property of this increment worth enforcing
// mechanically, because it erodes one convenience at a time.
func TestGoTreeIsFreeOfUseCaseVocabulary(t *testing.T) {
	// Words that would only appear in Go if pack logic had leaked into it.
	// Deliberately includes the second pack's vocabulary too: a harness that
	// is generic for one use case and not the other is not generic.
	forbidden := []string{
		"posting", "greenhouse", "cover letter", "coverletter",
		"job-hunt", "jobhunt", "recruiter", "hackernews", "hacker news",
	}

	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// packs/ is where use-case vocabulary belongs. Skip docs and VCS.
			switch d.Name() {
			case "packs", ".git", "docs", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file names the forbidden words in order to forbid them.
		if strings.HasSuffix(path, "vocabulary_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(b))
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("%s contains use-case vocabulary %q; that belongs in a pack", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

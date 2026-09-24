package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The harness knows nothing about any use case. A pack supplies every word
// specific to what it does; the Go tree supplies none of them.
//
// Deliberately includes every pack's vocabulary: a harness that is generic for one use case and not another is
// not generic.
func TestGoTreeIsFreeOfUseCaseVocabulary(t *testing.T) {
	forbidden := []string{
		"posting", "greenhouse", "cover letter", "coverletter",
		"job-hunt", "jobhunt", "recruiter", "hackernews", "hacker news",
		"salary", "dealbreaker", "deal-breaker", "deal_breaker", "employer",
		"on-call", "on_call", "curriculum vitae", "résumé",
	}
	// Words short enough to occur inside unrelated identifiers are matched
	// only as whole words.
	forbiddenWords := regexp.MustCompile(`\bcv\b`)

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
		if w := forbiddenWords.FindString(lower); w != "" {
			t.Errorf("%s contains use-case vocabulary %q; that belongs in a pack", path, w)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

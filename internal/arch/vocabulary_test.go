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
// The Go tree may name what the harness does with data: record, judgement,
// decision, evidence, quote, claim, citation, verdict, subject. It never
// names what the data is about: roles, pay, employers, pipeline states, a CV.
// That is enforced as a deny-list, because the allowed side cannot be
// enumerated. Ordinary English the harness uses generically, such as
// "resume" (a session) or "rejected", is not listed.
//
// Deliberately includes every pack's vocabulary: a harness that is generic
// for one use case and not another is not generic.
var forbiddenVocabulary = []string{
	"posting", "greenhouse", "cover letter", "coverletter",
	"job-hunt", "jobhunt", "recruiter", "hackernews", "hacker news",
	"salary", "dealbreaker", "deal-breaker", "deal_breaker", "employer",
	"on-call", "on_call", "curriculum vitae", "résumé",
	"applicant", "hiring", "career", "vacanc",
	"ghosted", "not_applied",
}

// Words short enough to occur inside unrelated identifiers are matched only
// as whole words.
var forbiddenVocabularyWords = regexp.MustCompile(`\b(cv|jobs)\b`)

// checkFileVocabulary reports every forbidden word found in path's contents.
func checkFileVocabulary(t *testing.T, path string) {
	b, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Errorf("read %s: %v", path, readErr)
		return
	}
	lower := strings.ToLower(string(b))
	for _, word := range forbiddenVocabulary {
		if strings.Contains(lower, word) {
			t.Errorf("%s contains use-case vocabulary %q; that belongs in a pack", path, word)
		}
	}
	if w := forbiddenVocabularyWords.FindString(lower); w != "" {
		t.Errorf("%s contains use-case vocabulary %q; that belongs in a pack", path, w)
	}
}

func TestGoTreeIsFreeOfUseCaseVocabulary(t *testing.T) {
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
		checkFileVocabulary(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// The Svelte source is subject to the same rule: the UI is a rendering of
// whatever pack is running, and names nothing pack-specific.
func TestWebSourceIsFreeOfUseCaseVocabulary(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// gen/ holds generated protobuf bindings, not hand-written source.
			if d.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".svelte", ".ts", ".js":
		default:
			return nil
		}
		checkFileVocabulary(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

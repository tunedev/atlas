package packfile_test

import (
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// packQuestion is one question as a pack's YAML questions block shapes it,
// mirroring tools.questionSpec's shape (that parser is unexported).
type packQuestion struct {
	ID      string              `yaml:"id"`
	Type    string              `yaml:"type"`
	Ask     string              `yaml:"ask"`
	Options []string            `yaml:"options"`
	Levels  []string            `yaml:"levels"`
	Forms   map[string][]string `yaml:"forms"`
}

// decodePackQuestions decodes a pack's raw questions block into
// ports.Question, mirroring how tools.Judge's own unexported parser reads
// the same YAML shape: options and levels both become a question's
// Options, and forms carries through as a question's Forms.
func decodePackQuestions(t *testing.T, raw string) []ports.Question {
	t.Helper()
	var specs []packQuestion
	if err := yaml.Unmarshal([]byte(raw), &specs); err != nil {
		t.Fatalf("decode questions block: %v", err)
	}
	qs := make([]ports.Question, len(specs))
	for i, s := range specs {
		options := s.Options
		if len(s.Levels) > 0 {
			options = s.Levels
		}
		qs[i] = ports.Question{ID: s.ID, Kind: ports.Kind(s.Type), Ask: s.Ask, Options: options, Forms: s.Forms}
	}
	return qs
}

// TestEveryShippedJudgeEachPackPassesValidation loads every pack under
// packs/, decodes the questions block of each judge.each step exactly as the
// pack YAML shapes it (id, type, ask, options, levels, forms), and asserts
// app.AnswerSchema accepts the whole set. It fails if a shipped pack's
// option set shares an initial, which is exactly how a shipped pack's
// "seniority" levels once failed before its rewrite for this task.
//
// This lives here, in packfile's own test package, rather than in
// core/app: it needs packfile.Load to read a real pack file, and a core
// package's test must never import an adapter.
func TestEveryShippedJudgeEachPackPassesValidation(t *testing.T) {
	paths, err := filepath.Glob("../../../../packs/*.yaml")
	if err != nil {
		t.Fatalf("glob packs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped packs found")
	}
	checked := 0
	for _, path := range paths {
		b, err := packfile.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		for _, s := range b.Steps {
			if s.Tool != "judge.each" {
				continue
			}
			qs := decodePackQuestions(t, s.With["questions"])
			if len(qs) == 0 {
				t.Fatalf("%s step %q declares no questions", path, s.ID)
			}
			if _, err := app.AnswerSchema(qs); err != nil {
				t.Errorf("%s step %q: a shipped pack's own questions were rejected: %v", path, s.ID, err)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no shipped pack has a judge.each step; this test proves nothing")
	}
}

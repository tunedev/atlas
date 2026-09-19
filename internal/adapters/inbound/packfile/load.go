// Package packfile reads a pack from a YAML file into a blueprint. This is an
// inbound adapter: a file is one way a blueprint reaches the core, and packs
// held somewhere else would be another.
package packfile

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/domain"
)

// stepIDPattern is what a step id becomes at render time: a Go template map
// key reached as .steps.<id>. It must be a legal template identifier, which
// this single pattern covers along with a hyphen, a dot, whitespace, and
// empty in one check rather than separate special cases.
var stepIDPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type pack struct {
	Name  string            `yaml:"name"`
	Vars  map[string]string `yaml:"vars"`
	Steps []step            `yaml:"steps"`
}

type step struct {
	ID   string            `yaml:"id"`
	Tool string            `yaml:"tool"`
	With map[string]string `yaml:"with"`
}

// Load reads and validates a pack file.
//
// Validation happens here rather than at run time because a pack author's
// feedback loop is the whole point: a duplicate step id should be an error
// naming the file, not an output that silently went missing after a model
// call has already been paid for.
func Load(path string) (domain.Blueprint, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("packfile: read %s: %w", path, err)
	}

	var p pack
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return domain.Blueprint{}, fmt.Errorf("packfile: parse %s: %w", path, err)
	}
	if p.Name == "" {
		return domain.Blueprint{}, fmt.Errorf("packfile: %s has no name", path)
	}
	if len(p.Steps) == 0 {
		return domain.Blueprint{}, fmt.Errorf("packfile: %s has no steps", path)
	}

	seen := make(map[string]bool, len(p.Steps))
	steps := make([]domain.Step, 0, len(p.Steps))
	for i, s := range p.Steps {
		if s.ID == "" {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s step %d has no id", path, i)
		}
		if !stepIDPattern.MatchString(s.ID) {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s step %q: id must match %s, since it becomes a template map key", path, s.ID, stepIDPattern)
		}
		if seen[s.ID] {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s has two steps with id %q", path, s.ID)
		}
		if s.Tool == "" {
			return domain.Blueprint{}, fmt.Errorf("packfile: %s step %q has no tool", path, s.ID)
		}
		seen[s.ID] = true

		with := s.With
		if with == nil {
			with = map[string]string{}
		}
		steps = append(steps, domain.Step{ID: s.ID, Tool: s.Tool, With: with})
	}

	vars := p.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	return domain.Blueprint{Name: p.Name, Vars: vars, Steps: steps}, nil
}

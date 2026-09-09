// Package app runs blueprints: it resolves each step's tool, renders that
// step's config against accumulated state, executes, and records the result.
package app

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/tunedev/atlas/internal/core/domain"
)

// Render evaluates one config value as a Go template against state. Packs
// reach earlier output through .steps.<id>.<path> and their own variables
// through .vars.<name>.
//
// Option "missingkey=error" is the whole reason this is not a one-liner. The
// default emits "<no value>" into the string, which for a prompt means the
// model is asked to work from a gap and answers anyway — a silently wrong
// result rather than a failed run.
//
// Deliberately no conditionals, loops, or expression language: a blueprint is
// a sequence, not a program. The cheapest way to learn what expressiveness a
// pack actually needs is to run out of it with a real pack in hand.
func Render(tmpl string, s *domain.State) (string, error) {
	t, err := template.New("with").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("render: parse %q: %w", tmpl, err)
	}

	data := map[string]any{
		"vars":  s.Vars(),
		"steps": s.Outputs(),
	}

	var out strings.Builder
	if err := t.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render: execute %q: %w", tmpl, err)
	}
	return out.String(), nil
}

// Select narrows a tool's result by a dotted path before it is stored, so a
// pack can keep the one item it cares about rather than a whole response.
// Numeric segments index a slice; everything else is a map key. An empty path
// returns the value unchanged.
func Select(value any, path string) (any, error) {
	if path == "" {
		return value, nil
	}
	current := value
	for _, segment := range strings.Split(path, ".") {
		next, err := descend(current, segment)
		if err != nil {
			return nil, fmt.Errorf("select %q: %w", path, err)
		}
		current = next
	}
	return current, nil
}

func descend(value any, segment string) (any, error) {
	if i, err := strconv.Atoi(segment); err == nil {
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("segment %q indexes a %T, not a list", segment, value)
		}
		if i < 0 || i >= len(items) {
			return nil, fmt.Errorf("index %d is out of range, length %d", i, len(items))
		}
		return items[i], nil
	}

	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("segment %q reads a %T, not an object", segment, value)
	}
	found, ok := fields[segment]
	if !ok {
		return nil, fmt.Errorf("no key %q", segment)
	}
	return found, nil
}

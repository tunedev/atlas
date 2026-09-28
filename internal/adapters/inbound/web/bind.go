package web

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// bindings is every value a var may come from; state is the screen's cached
// last result, decoded from JSON.
type bindings struct {
	params, inputs, server map[string]string
	state                  any
}

// resolve turns vars, a screen's or an action's binding map, into the
// concrete values a pack's Vars need.
func resolve(vars map[string]string, b bindings) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for name, binding := range vars {
		v, err := resolveOne(binding, b)
		if err != nil {
			return nil, fmt.Errorf("web: var %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

func resolveOne(binding string, b bindings) (string, error) {
	source, rest, _ := strings.Cut(binding, ".")
	switch source {
	case "param":
		return takeValue(b.params, rest)
	case "input":
		return takeValue(b.inputs, rest)
	case "server":
		return takeValue(b.server, rest)
	case "const":
		return rest, nil
	case "state":
		v, ok := lookup(b.state, rest)
		if !ok {
			return "", fmt.Errorf("no value at state.%s", rest)
		}
		return stateValue(v), nil
	default:
		return "", fmt.Errorf("unknown source %q", binding)
	}
}

func takeValue(values map[string]string, name string) (string, error) {
	v, ok := values[name]
	if !ok {
		return "", fmt.Errorf("no value for %q", name)
	}
	return v, nil
}

// stateValue renders a looked-up state value as a string: a string result is
// used as is, anything else is JSON-encoded.
func stateValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// lookup walks a dotted path through v: a map segment indexes by key, a
// numeric segment indexes a slice.
func lookup(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// checkInputs reports an error if any of values fails the Options an action
// declares for the input of the same name. A later task validates a
// submitted form against this before calling resolve.
func (a Action) checkInputs(values map[string]string) error {
	for _, in := range a.Input {
		if len(in.Options) == 0 {
			continue
		}
		v, ok := values[in.Name]
		if !ok {
			continue
		}
		if !slices.Contains(in.Options, v) {
			return fmt.Errorf("web: input %q: %q is not one of %v", in.Name, v, in.Options)
		}
	}
	return nil
}

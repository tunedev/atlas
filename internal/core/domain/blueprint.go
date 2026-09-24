package domain

import "fmt"

// Blueprint is a parsed pack: an ordered list of steps plus the variables its
// templates may reference.
//
// Nothing here describes a use case. What a blueprint does lives entirely in
// the tool names its steps call and the config they carry.
type Blueprint struct {
	Name  string
	Vars  map[string]string
	Steps []Step
}

// Step names a tool and supplies its configuration. With values are Go
// template source, rendered against State immediately before the tool runs,
// so a step can reference what earlier steps produced.
type Step struct {
	ID   string
	Tool string
	With map[string]string
}

// WithVars returns a copy of b whose vars are overridden by overrides. Every
// name in overrides must already be declared by the pack, so a misspelt name
// is an error rather than a silently ignored value.
func (b Blueprint) WithVars(overrides map[string]string) (Blueprint, error) {
	vars := make(map[string]string, len(b.Vars))
	for k, v := range b.Vars {
		vars[k] = v
	}
	for k, v := range overrides {
		if _, ok := vars[k]; !ok {
			return Blueprint{}, fmt.Errorf("blueprint %s: no var named %q to override", b.Name, k)
		}
		vars[k] = v
	}
	b.Vars = vars
	return b, nil
}

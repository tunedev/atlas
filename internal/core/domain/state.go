package domain

// State is what a blueprint accumulates as it runs: the variables it started
// with, and one output per completed step, keyed by step id.
//
// A pointer with unexported maps rather than a value type: the runner appends
// to it step by step, and templates read it by path. Both maps are non-nil
// from construction so reading Outputs or Vars before any step has run never
// panics on a nil map. A template referencing a step that has not run yet
// still fails: Render's missingkey=error rejects the absent key rather than
// rendering it empty.
type State struct {
	vars    map[string]string
	outputs map[string]any
}

func NewState(vars map[string]string) *State {
	if vars == nil {
		vars = map[string]string{}
	}
	return &State{vars: vars, outputs: map[string]any{}}
}

func (s *State) Vars() map[string]string {
	vars := make(map[string]string, len(s.vars))
	for k, v := range s.vars {
		vars[k] = v
	}
	return vars
}

// Outputs returns a shallow copy of step outputs. The copy protects the map
// itself but not nested values: a caller can mutate a map inside a value.
func (s *State) Outputs() map[string]any {
	outputs := make(map[string]any, len(s.outputs))
	for k, v := range s.outputs {
		outputs[k] = v
	}
	return outputs
}

// Put records a step's output under its id, replacing any previous value.
func (s *State) Put(stepID string, out any) { s.outputs[stepID] = out }

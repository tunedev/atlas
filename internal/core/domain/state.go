package domain

// State is what a blueprint accumulates as it runs: the variables it started
// with, and one output per completed step, keyed by step id.
//
// A pointer with unexported maps rather than a value type: the runner appends
// to it step by step, and templates read it by path. Both maps are non-nil
// from construction so a template referencing a step that has not run yet
// renders empty rather than panicking.
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

func (s *State) Vars() map[string]string { return s.vars }

func (s *State) Outputs() map[string]any { return s.outputs }

// Put records a step's output under its id, replacing any previous value.
func (s *State) Put(stepID string, out any) { s.outputs[stepID] = out }

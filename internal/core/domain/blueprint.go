package domain

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

package tools

import "github.com/tunedev/atlas/internal/core/ports"

// Registry resolves a tool name to its implementation. Adding a tool is
// adding it to this map at the composition root; nothing switches on a tool
// name anywhere else.
type Registry map[string]ports.Tool

func NewRegistry(list ...ports.Tool) Registry {
	r := make(Registry, len(list))
	for _, t := range list {
		r[t.Name()] = t
	}
	return r
}

func (r Registry) Lookup(name string) (ports.Tool, bool) {
	t, ok := r[name]
	return t, ok
}

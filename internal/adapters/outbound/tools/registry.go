package tools

import (
	"maps"

	"github.com/tunedev/atlas/internal/core/ports"
)

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

// With returns a copy of r that also holds t. r itself is unchanged, so a
// registry handed out earlier never gains tools behind its holder's back.
func (r Registry) With(t ports.Tool) Registry {
	out := make(Registry, len(r)+1)
	maps.Copy(out, r)
	out[t.Name()] = t
	return out
}

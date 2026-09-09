package ports

import "context"

// Tool is one named unit of capability. The harness ships generic tools; a
// pack calls them by name and supplies their configuration.
//
// with arrives fully rendered: the runner has already evaluated every
// template against accumulated state, so a tool never sees template source
// and never reads state. That is what keeps tools generic.
//
// The return is any because a tool's result shape is its own business, and
// packs narrow it by path rather than the harness knowing its type.
type Tool interface {
	Name() string
	Invoke(ctx context.Context, with map[string]string) (any, error)
}

// Registry resolves a tool name to its implementation. Adding a tool is
// adding a registry entry; nothing dispatches on a name anywhere else.
type Registry interface {
	Lookup(name string) (Tool, bool)
}

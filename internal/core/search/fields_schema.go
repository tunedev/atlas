package search

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// Schema returns the JSON Schema of an enrichment's values: every declared
// field nested by path segment, required and nullable, and nothing else.
func (f Fields) Schema() []byte {
	root := map[string]any{}
	for path, fd := range f.ByPath {
		node := root
		segs := strings.Split(path, ".")
		for _, seg := range segs[:len(segs)-1] {
			child, ok := node[seg].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[seg] = child
			}
			node = child
		}
		node[segs[len(segs)-1]] = leafSchema(fd)
	}
	s := objectSchema(root)
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	// A tree of maps, slices, strings and numbers always marshals.
	b, _ := json.Marshal(s)
	return b
}

// objectSchema turns a tree whose leaves are schemas into object schemas.
// A leaf is told apart from a branch by its "type" or "enum" keyword.
func objectSchema(node map[string]any) map[string]any {
	props := map[string]any{}
	for name, v := range node {
		child := v.(map[string]any)
		if isLeaf(child) {
			props[name] = child
			continue
		}
		props[name] = objectSchema(child)
	}
	return map[string]any{
		"type":                 "object",
		"required":             slices.Sorted(maps.Keys(props)),
		"additionalProperties": false,
		"properties":           props,
	}
}

func isLeaf(node map[string]any) bool {
	_, typed := node["type"]
	_, enum := node["enum"]
	return typed || enum
}

func leafSchema(fd Field) map[string]any {
	switch fd.Kind {
	case "scalar":
		if len(fd.Enum) > 0 {
			return map[string]any{"enum": append(toAny(fd.Enum), nil)}
		}
		return nullable("string", word(fd))
	case "bool":
		return nullable("boolean", nil)
	case "number":
		return nullable("number", nil)
	case "list":
		return nullable("array", map[string]any{"minItems": 1, "items": item(fd)})
	case "range":
		return nullable("array", map[string]any{"minItems": 2, "maxItems": 2,
			"items": map[string]any{"type": "integer", "minimum": *fd.Min, "maximum": *fd.Max}})
	case "texts":
		return nullable("array", map[string]any{"minItems": 1, "items": map[string]any{"type": "string"}})
	}
	return nullable("string", nil)
}

func nullable(typ string, keywords map[string]any) map[string]any {
	s := map[string]any{"type": []string{typ, "null"}}
	maps.Copy(s, keywords)
	return s
}

func item(fd Field) map[string]any {
	s := map[string]any{"type": "string"}
	if len(fd.Enum) > 0 {
		s["enum"] = fd.Enum
	}
	maps.Copy(s, word(fd))
	return s
}

// word returns the pattern keyword a string must match: the declared
// pattern, or no uppercase for a canonical field.
func word(fd Field) map[string]any {
	switch {
	case fd.Pattern != "":
		return map[string]any{"pattern": fd.Pattern}
	case fd.Canonical:
		return map[string]any{"pattern": "^[^A-Z]+$"}
	}
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

package app

import "sort"

// spansKey and citationsKey are the structural convention a schema uses for
// citing spans by id, and the resolved citations that replace them.
const (
	spansKey     = "spans"
	citationsKey = "citations"
)

// ResolveCitations replaces every "spans" list in tree with "citations":
// one {"id", "quote"} per cited id, quote being the span's verbatim text. An
// id that is not a whole number naming a span gets an empty quote, which
// Ground then reports. A "spans" value that is not a list resolves to no
// citations. tree is modified in place and returned.
func ResolveCitations(tree any, spans []Span) any {
	byID := make(map[int]string, len(spans))
	for _, s := range spans {
		byID[s.ID] = s.Text
	}
	eachObject(tree, func(obj map[string]any) {
		raw, ok := obj[spansKey]
		if !ok {
			return
		}
		delete(obj, spansKey)
		ids, _ := raw.([]any)
		citations := make([]any, 0, len(ids))
		for _, id := range ids {
			citations = append(citations, map[string]any{"id": id, "quote": quoteFor(id, byID)})
		}
		obj[citationsKey] = citations
	})
	return tree
}

// quoteFor is the text of the span id names, or "" if id names none.
func quoteFor(id any, byID map[int]string) string {
	f, ok := id.(float64)
	if !ok || f != float64(int(f)) {
		return ""
	}
	return byID[int(f)]
}

// eachObject calls fn for every object in v, parents before children, in
// a fixed order: object keys sorted, array elements in order.
func eachObject(v any, fn func(map[string]any)) {
	switch val := v.(type) {
	case map[string]any:
		fn(val)
		for _, k := range sortedKeys(val) {
			eachObject(val[k], fn)
		}
	case []any:
		for _, e := range val {
			eachObject(e, fn)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Settle decides, for every object with citations, which citations survive
// and whether the object is a gap. A citation survives if it is grounded
// and, on an object with a non-empty "text", judged relevant: a statement
// never judged is a gap, not a claim. kept lists, per top-level key and in
// order, what a renderer shows for each object that is not a gap: its text,
// or its surviving quotes when it has none. Every top-level key is present
// in kept, empty when nothing under it survived. tree is modified in place.
func Settle(tree any) (any, map[string][]string, int) {
	kept := map[string][]string{}
	gaps := 0
	top, ok := tree.(map[string]any)
	if !ok {
		return tree, kept, 0
	}
	for _, key := range sortedKeys(top) {
		kept[key] = []string{}
		eachObject(top[key], func(obj map[string]any) {
			raw, ok := obj[citationsKey].([]any)
			if !ok {
				return
			}
			text, _ := obj["text"].(string)
			var survivors []any
			var quotes []string
			for _, r := range raw {
				c, ok := r.(map[string]any)
				if !ok || c["status"] != "grounded" {
					continue
				}
				if text != "" && c["relevant"] != true {
					continue
				}
				survivors = append(survivors, c)
				if q, ok := c["quote"].(string); ok {
					quotes = append(quotes, q)
				}
			}
			if survivors == nil {
				survivors = []any{}
			}
			obj[citationsKey] = survivors
			obj["gap"] = len(survivors) == 0
			if len(survivors) == 0 {
				gaps++
				return
			}
			if text != "" {
				kept[key] = append(kept[key], text)
				return
			}
			kept[key] = append(kept[key], quotes...)
		})
	}
	return tree, kept, gaps
}

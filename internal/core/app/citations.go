package app

import (
	"sort"
	"strings"
)

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

// statement reports whether obj is a statement (it has a "text" key) and
// its trimmed text. A text that is not a string, or is only whitespace, is
// "": a statement with no text, which is always a gap.
func statement(obj map[string]any) (string, bool) {
	raw, has := obj["text"]
	if !has {
		return "", false
	}
	text, _ := raw.(string)
	return strings.TrimSpace(text), true
}

// Settled is what Settle decides about a document.
type Settled struct {
	// Tree is the document, every citation-bearing object and statement
	// carrying its surviving citations and its gap flag.
	Tree any
	// Kept lists, per top-level key and in order, what a renderer shows for
	// each object that is not a gap: its text, or its surviving quotes when
	// it has none.
	Kept map[string][]string
	// GapsText lists, per top-level key and in order, the text of every
	// statement that is a gap and has text.
	GapsText map[string][]string
	// GapsAll is every GapsText list joined, top-level keys in sorted order.
	GapsAll []string
	// Gaps counts every object that is a gap.
	Gaps int
}

// Settle decides, for every statement and every object with citations,
// which citations survive and whether the object is a gap. A statement is
// any object with a "text" key: it is a gap unless its trimmed text is
// non-empty and it has a citation that is grounded and judged relevant; one
// with no citations list is a gap. Settle always sets a statement's gap flag
// and writes back its trimmed text. An object with citations but no text
// keeps citations on grounding alone. Kept and GapsText hold every top-level
// key, empty when nothing under it applies. tree is modified in place.
func Settle(tree any) Settled {
	s := Settled{Tree: tree, Kept: map[string][]string{}, GapsText: map[string][]string{}, GapsAll: []string{}}
	top, ok := tree.(map[string]any)
	if !ok {
		return s
	}
	for _, key := range sortedKeys(top) {
		s.Kept[key] = []string{}
		s.GapsText[key] = []string{}
		eachObject(top[key], func(obj map[string]any) {
			text, isStatement := statement(obj)
			raw, hasCitations := obj[citationsKey].([]any)
			if !isStatement && !hasCitations {
				return
			}
			if isStatement {
				if _, isString := obj["text"].(string); isString {
					obj["text"] = text
				}
			}
			survivors, quotes := surviving(raw, isStatement)
			if isStatement && text == "" {
				survivors, quotes = []any{}, nil
			}
			obj[citationsKey] = survivors
			obj["gap"] = len(survivors) == 0
			switch {
			case len(survivors) == 0:
				s.Gaps++
				if text != "" {
					s.GapsText[key] = append(s.GapsText[key], text)
					s.GapsAll = append(s.GapsAll, text)
				}
			case text != "":
				s.Kept[key] = append(s.Kept[key], text)
			default:
				s.Kept[key] = append(s.Kept[key], quotes...)
			}
		})
	}
	return s
}

// surviving returns the citations in raw that are grounded and, for a
// statement, judged relevant, with their quotes. It never returns nil.
func surviving(raw []any, isStatement bool) ([]any, []string) {
	survivors := []any{}
	var quotes []string
	for _, r := range raw {
		c, ok := r.(map[string]any)
		if !ok || c["status"] != "grounded" {
			continue
		}
		if isStatement && c["relevant"] != true {
			continue
		}
		survivors = append(survivors, c)
		if q, ok := c["quote"].(string); ok {
			quotes = append(quotes, q)
		}
	}
	return survivors, quotes
}

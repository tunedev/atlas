package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// quoteKey is the structural convention a schema uses to tie a value to the
// verbatim source text that supports it.
const quoteKey = "quote"

// Ground reports, as a JSON Pointer per field, every "quote" value in
// extracted that is not a verbatim (case-insensitive, whitespace-normalised)
// substring of source. An empty or non-string quote is never grounded. It
// knows nothing about what a quote belongs to; it only looks for the key.
func Ground(extracted json.RawMessage, source string) ([]string, error) {
	var tree any
	if err := json.Unmarshal(extracted, &tree); err != nil {
		return nil, fmt.Errorf("ground: %w", err)
	}
	haystack := normaliseText(source)
	var ungrounded []string
	eachQuoted(tree, "", func(obj map[string]any, pointer string) {
		if !grounded(obj[quoteKey], haystack) {
			ungrounded = append(ungrounded, pointer+"/"+quoteKey)
		}
	})
	return ungrounded, nil
}

// Annotate sets "status" to "grounded" or "needs_review" beside every quote
// in extracted, replacing any status already there, and returns the tree and
// how many need review. Nothing is dropped: a flagged value is kept for a
// person to correct.
func Annotate(extracted json.RawMessage, source string) (any, int, error) {
	var tree any
	if err := json.Unmarshal(extracted, &tree); err != nil {
		return nil, 0, fmt.Errorf("ground: %w", err)
	}
	haystack := normaliseText(source)
	flagged := 0
	eachQuoted(tree, "", func(obj map[string]any, _ string) {
		if grounded(obj[quoteKey], haystack) {
			obj["status"] = "grounded"
			return
		}
		obj["status"] = "needs_review"
		flagged++
	})
	return tree, flagged, nil
}

// eachQuoted calls fn for every object in v that has a quote key, with the
// object's JSON Pointer.
func eachQuoted(v any, pointer string, fn func(map[string]any, string)) {
	switch val := v.(type) {
	case map[string]any:
		if _, ok := val[quoteKey]; ok {
			fn(val, pointer)
		}
		for k, e := range val {
			eachQuoted(e, pointer+"/"+escapePointer(k), fn)
		}
	case []any:
		for i, e := range val {
			eachQuoted(e, pointer+"/"+strconv.Itoa(i), fn)
		}
	}
}

// grounded reports whether quote is a non-empty string whose normalised form
// occurs in haystack.
func grounded(quote any, haystack string) bool {
	s, ok := quote.(string)
	if !ok {
		return false
	}
	needle := normaliseText(s)
	return needle != "" && strings.Contains(haystack, needle)
}

// normaliseText lowercases s and collapses every whitespace run to one space.
func normaliseText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// escapePointer escapes a key as one RFC 6901 JSON Pointer segment.
func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

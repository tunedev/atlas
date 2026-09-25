package app

import (
	"strings"
	"unicode"
)

// Dedupe keeps the first of every group of items that agree on all of
// fields, compared after normalising: case folded, anything but letters and
// digits treated as a space, whitespace collapsed. An item that is not an
// object, or lacks a key field, is kept and never merged, since nothing
// proves it a duplicate. It returns the kept items in order, and how many
// were merged away.
func Dedupe(items []any, fields []string) ([]any, int) {
	seen := map[string]bool{}
	kept := make([]any, 0, len(items))
	for _, it := range items {
		key, ok := dedupeKey(it, fields)
		if ok && seen[key] {
			continue
		}
		if ok {
			seen[key] = true
		}
		kept = append(kept, it)
	}
	return kept, len(items) - len(kept)
}

func dedupeKey(item any, fields []string) (string, bool) {
	obj, ok := item.(map[string]any)
	if !ok {
		return "", false
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		s, ok := obj[f].(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", false
		}
		parts[i] = normaliseKey(s)
	}
	return strings.Join(parts, "\x00"), true
}

func normaliseKey(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

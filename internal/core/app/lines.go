package app

import "strings"

// LineItems makes one {"quote", "status": "grounded"} item per non-blank
// line of text, trimmed, in order: each quote is the text's own words, so it
// is grounded by construction. Blank text gives an empty list.
func LineItems(text string) []any {
	items := []any{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			items = append(items, map[string]any{"quote": line, "status": "grounded"})
		}
	}
	return items
}

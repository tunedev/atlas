package app

import "fmt"

// CiteItems pairs each grounded item with the spans a citing step named for
// it. items is a list of {"quote", "status"} objects, in the order asked;
// cited is a list of {"item", "spans"} objects naming an item by its index
// into items. For every grounded item, in order, CiteItems emits one
// {"text", "spans"} object: text is the item's own quote, spans is the
// union, in first-seen order with duplicates removed, of every cited
// entry's spans for that item's index (empty when nothing cited it). An
// item that is not grounded is dropped and counted in dropped, never
// emitted. A cited entry naming an item index that is not a whole number, or
// that names no item, is ignored.
func CiteItems(items []any, cited []any) ([]any, int) {
	spans := spansByItem(cited, len(items))

	out := make([]any, 0, len(items))
	dropped := 0
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		if item["status"] != "grounded" {
			dropped++
			continue
		}
		out = append(out, map[string]any{"text": item["quote"], "spans": spans[i]})
	}
	return out, dropped
}

// spansByItem unions, per item index, every cited entry's spans naming that
// index, in first-seen order with duplicates removed. Every index in
// [0, n) is present, empty when nothing cited it.
func spansByItem(cited []any, n int) map[int][]any {
	result := make(map[int][]any, n)
	seen := make(map[int]map[string]bool, n)
	for i := 0; i < n; i++ {
		result[i] = []any{}
		seen[i] = map[string]bool{}
	}
	for _, raw := range cited {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := wholeIndex(entry["item"], n)
		if !ok {
			continue
		}
		spanList, _ := entry["spans"].([]any)
		for _, s := range spanList {
			key := fmt.Sprint(s)
			if seen[idx][key] {
				continue
			}
			seen[idx][key] = true
			result[idx] = append(result[idx], s)
		}
	}
	return result
}

// wholeIndex reports v as a valid item index: a whole number in [0, n).
// Anything else, including a fractional number, is not.
func wholeIndex(v any, n int) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	idx := int(f)
	if idx < 0 || idx >= n {
		return 0, false
	}
	return idx, true
}

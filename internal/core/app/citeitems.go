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

// GatherItems pairs each grounded item with the sentences an answering step
// wrote for it. items is a list of {"quote", "status"} objects, in the order
// asked; answered is a list of {"item", "sentences"} objects naming an item
// by its index into items. For every grounded item, in order, GatherItems
// emits one {"item", "sentences"} object: item is the item's own quote,
// sentences every sentence object the answered entries give for its index,
// in order. An item nothing answers gets one sentence with empty text, which
// Settle marks a gap, so an unanswered item is counted, never silently
// absent. An item that is not grounded is dropped and counted in dropped.
// An entry naming no item by a whole index, and a sentence that is not an
// object, are ignored.
func GatherItems(items []any, answered []any) ([]any, int) {
	sentences := sentencesByItem(answered, len(items))
	out := make([]any, 0, len(items))
	dropped := 0
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		if item["status"] != "grounded" {
			dropped++
			continue
		}
		said := sentences[i]
		if len(said) == 0 {
			said = []any{map[string]any{"text": "", "spans": []any{}}}
		}
		out = append(out, map[string]any{"item": item["quote"], "sentences": said})
	}
	return out, dropped
}

// sentencesByItem collects, per item index in [0, n), every sentence object
// the answered entries give for it, in order.
func sentencesByItem(answered []any, n int) map[int][]any {
	result := make(map[int][]any, n)
	for _, raw := range answered {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := wholeIndex(entry["item"], n)
		if !ok {
			continue
		}
		list, _ := entry["sentences"].([]any)
		for _, s := range list {
			if obj, ok := s.(map[string]any); ok {
				result[idx] = append(result[idx], obj)
			}
		}
	}
	return result
}

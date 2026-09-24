package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/tunedev/atlas/internal/core/ports"
)

// AnswerTokens locates, for each id in ids, the token in c that carries that
// field's answer.
//
// A completion answering several questions packs every answer into one JSON
// object, so reading the last class-bearing token (as MassPerClass does) is
// not enough: a later question's answer would be read as an earlier one's.
// Here the completion's text is reconstructed from its tokens, recording the
// byte offset where each token starts, and walked with encoding/json's
// Decoder, which reports an input offset after each token it reads. For each
// top-level key, the value occupies the bytes between the offset after the
// key and the offset after the value. The field's answer token is the first
// token starting in that range whose text carries a character other than
// whitespace, `"`, `:` and `,` -- the punctuation around a value is
// structure, and the first token with content is where the alternatives
// distinguish one option from another.
//
// Fields present in the completion but not among ids are ignored:
// additionalProperties: false already forbids them, and a model that emits
// one anyway should not lose the answers that are present.
func AnswerTokens(c ports.Completion, ids []string) (map[string]ports.Token, error) {
	if len(c.Tokens) == 0 {
		return nil, fmt.Errorf("judge: completion carries no per-token data")
	}

	text, starts := reconstruct(c.Tokens)

	ranges, err := fieldRanges(text)
	if err != nil {
		return nil, err
	}

	out := make(map[string]ports.Token, len(ids))
	for _, id := range ids {
		r, ok := ranges[id]
		if !ok {
			return nil, fmt.Errorf("judge: completion did not answer %q", id)
		}
		tok, ok := firstContentToken(c.Tokens, starts, r)
		if !ok {
			return nil, fmt.Errorf("judge: no answer token found for %q", id)
		}
		out[id] = tok
	}
	return out, nil
}

// reconstruct concatenates each token's text into the completion's full
// text, recording the byte offset where each token starts.
func reconstruct(tokens []ports.Token) (string, []int) {
	var sb strings.Builder
	starts := make([]int, len(tokens))
	for i, t := range tokens {
		starts[i] = sb.Len()
		sb.WriteString(t.Text)
	}
	return sb.String(), starts
}

// byteRange is the half-open byte range [Start, End) a field's value
// occupies in the reconstructed text.
type byteRange struct {
	Start, End int
}

// fieldRanges walks text as a JSON object and returns, per top-level key,
// the byte range its value occupies.
func fieldRanges(text string) (map[string]byteRange, error) {
	dec := json.NewDecoder(strings.NewReader(text))

	open, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("judge: completion is not a JSON object: %w", err)
	}
	if d, ok := open.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("judge: completion is not a JSON object")
	}

	ranges := make(map[string]byteRange)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("judge: completion is not a JSON object: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("judge: completion is not a JSON object")
		}

		start := int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("judge: completion is not a JSON object: %w", err)
		}
		ranges[key] = byteRange{Start: start, End: int(dec.InputOffset())}
	}
	return ranges, nil
}

// firstContentToken returns the first token, in order, whose start falls
// within r and whose text carries a character other than whitespace, `"`,
// `:` and `,`.
func firstContentToken(tokens []ports.Token, starts []int, r byteRange) (ports.Token, bool) {
	for i, tok := range tokens {
		if starts[i] < r.Start || starts[i] >= r.End {
			continue
		}
		if hasContent(tok.Text) {
			return tok, true
		}
	}
	return ports.Token{}, false
}

// hasContent reports whether s carries a character other than whitespace,
// `"`, `:` and `,`.
func hasContent(s string) bool {
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), r == '"', r == ':', r == ',':
			continue
		default:
			return true
		}
	}
	return false
}

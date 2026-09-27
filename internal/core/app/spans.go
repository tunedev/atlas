package app

import (
	"fmt"
	"regexp"
	"strings"
)

// SpanSource is one named text to split into spans.
type SpanSource struct {
	Name string
	Text string
}

// Span is one citable piece of source text. IDs are numbered across all
// sources in the order given, so the same inputs always give the same IDs.
type Span struct {
	ID     int    `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// spanBreak is where one span ends: a line break or a bullet glyph.
var spanBreak = regexp.MustCompile("[\n●•▪◦]")

// Spans splits each source at line breaks and bullet glyphs, collapses
// whitespace, and keeps the pieces at least minChars long.
func Spans(sources []SpanSource, minChars int) []Span {
	var spans []Span
	for _, src := range sources {
		for _, piece := range spanBreak.Split(src.Text, -1) {
			text := strings.Join(strings.Fields(piece), " ")
			if len(text) < minChars {
				continue
			}
			spans = append(spans, Span{ID: len(spans), Source: src.Name, Text: text})
		}
	}
	return spans
}

// SpanListing is spans as a prompt carries them: one "[id] text" per line.
func SpanListing(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		fmt.Fprintf(&b, "[%d] %s\n", s.ID, s.Text)
	}
	return b.String()
}

// SpanText is every span's text, one per line: the source a cited quote is
// grounded against.
func SpanText(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

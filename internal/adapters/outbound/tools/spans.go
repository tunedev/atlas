package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
)

// TextSpans splits local text files into numbered spans a model can cite by
// id. A directory contributes its regular files, sorted by name. Paths must
// not contain spaces.
type TextSpans struct {
	maxBytes int64
}

func NewTextSpans(maxBytes int64) *TextSpans { return &TextSpans{maxBytes: maxBytes} }

func (s *TextSpans) Name() string { return "text.spans" }

func (s *TextSpans) Invoke(_ context.Context, with map[string]string) (any, error) {
	minChars, err := strconv.Atoi(with["min_chars"])
	if err != nil {
		return nil, fmt.Errorf("text.spans: min_chars: %w", err)
	}
	var sources []app.SpanSource
	for _, path := range strings.Fields(with["paths"]) {
		found, err := s.read(path)
		if err != nil {
			return nil, fmt.Errorf("text.spans: %w", err)
		}
		sources = append(sources, found...)
	}
	spans := app.Spans(sources, minChars)
	if len(spans) == 0 {
		return nil, fmt.Errorf("text.spans: no span of at least %d characters in %q", minChars, with["paths"])
	}
	return map[string]any{"spans": spans, "listing": app.SpanListing(spans), "source": app.SpanText(spans)}, nil
}

// read returns path as one source, or each regular file in it if it is a
// directory.
func (s *TextSpans) read(path string) ([]app.SpanSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		body, err := readFile(path, s.maxBytes)
		if err != nil {
			return nil, err
		}
		return []app.SpanSource{{Name: filepath.Base(path), Text: string(body)}}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []app.SpanSource
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		body, err := readFile(filepath.Join(path, e.Name()), s.maxBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, app.SpanSource{Name: e.Name(), Text: string(body)})
	}
	return out, nil
}

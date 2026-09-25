package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tsawler/tabula"
)

// FileText extracts plain text from a PDF or DOCX file, sniffing the format
// from the file itself. A file with no text layer, such as a scan, is an
// error: extracting from an empty string would ask a model to work from
// nothing. Warnings from the reader are passed through, never dropped.
type FileText struct {
	maxBytes int64
}

func NewFileText(maxBytes int64) *FileText { return &FileText{maxBytes: maxBytes} }

func (f *FileText) Name() string { return "file.text" }

func (f *FileText) Invoke(_ context.Context, with map[string]string) (any, error) {
	path := with["path"]
	body, err := readFile(path, f.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("file.text: %w", err)
	}

	text, warnings, err := tabula.Open(path).Text()
	if err != nil {
		return nil, fmt.Errorf("file.text: %s: %w", path, err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("file.text: %s has no extractable text; a scanned document needs OCR, which is not supported", path)
	}

	sum := sha256.Sum256(body)
	return map[string]any{
		"text":     text,
		"warnings": warningStrings(warnings),
		"sha256":   hex.EncodeToString(sum[:]),
		"name":     filepath.Base(path),
	}, nil
}

// warningStrings renders each reader warning as text.
func warningStrings(ws []tabula.Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.String()
	}
	return out
}

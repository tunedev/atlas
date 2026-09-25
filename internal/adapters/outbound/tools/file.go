package tools

import (
	"context"
	"fmt"
	"os"
)

// FileRead reads a local file as text. A file over maxBytes fails the call
// rather than being truncated: a later step must never commit part of a file
// believing it whole.
type FileRead struct {
	maxBytes int64
}

func NewFileRead(maxBytes int64) *FileRead { return &FileRead{maxBytes: maxBytes} }

func (f *FileRead) Name() string { return "file.read" }

func (f *FileRead) Invoke(_ context.Context, with map[string]string) (any, error) {
	body, err := readFile(with["path"], f.maxBytes)
	if err != nil {
		return nil, fmt.Errorf("file.read: %w", err)
	}
	return map[string]any{"body": string(body)}, nil
}

// readFile reads at most maxBytes from path, erroring if there is more.
func readFile(path string, maxBytes int64) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("no path")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := readLimited(f, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return body, nil
}

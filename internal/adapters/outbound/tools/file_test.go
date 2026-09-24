package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func writeFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestFileReadReturnsTheBytesAsText(t *testing.T) {
	path := writeFile(t, "log.json", []byte(`{"wind":"west"}`))
	out, err := tools.NewFileRead(1024).Invoke(context.Background(), map[string]string{"path": path})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.(map[string]any)["body"] != `{"wind":"west"}` {
		t.Errorf("out = %v", out)
	}
}

func TestFileReadRefusesAFileOverTheLimitRatherThanTruncating(t *testing.T) {
	path := writeFile(t, "big.txt", []byte(strings.Repeat("x", 11)))
	_, err := tools.NewFileRead(10).Invoke(context.Background(), map[string]string{"path": path})
	if err == nil {
		t.Fatal("an over-limit file was read; a truncated body would be committed as complete")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("error does not name the limit: %v", err)
	}
}

func TestFileReadAtExactlyTheLimitSucceeds(t *testing.T) {
	path := writeFile(t, "edge.txt", []byte(strings.Repeat("x", 10)))
	if _, err := tools.NewFileRead(10).Invoke(context.Background(), map[string]string{"path": path}); err != nil {
		t.Fatalf("an at-limit file was refused: %v", err)
	}
}

func TestFileReadOfAMissingFileNamesThePath(t *testing.T) {
	_, err := tools.NewFileRead(10).Invoke(context.Background(), map[string]string{"path": "/no/such/file"})
	if err == nil || !strings.Contains(err.Error(), "/no/such/file") || !strings.HasPrefix(err.Error(), "file.read: ") {
		t.Errorf("err = %v", err)
	}
}

package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
)

func TestSpansReadFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "log.txt"), []byte("Kept the north light burning\n"), 0o600))
	notes := filepath.Join(dir, "notes")
	must(t, os.Mkdir(notes, 0o700))
	must(t, os.WriteFile(filepath.Join(notes, "b.txt"), []byte("Second note about the weather"), 0o600))
	must(t, os.WriteFile(filepath.Join(notes, "a.txt"), []byte("First note about the tides"), 0o600))

	out, err := tools.NewTextSpans(1<<20).Invoke(context.Background(), map[string]string{
		"paths":     filepath.Join(dir, "log.txt") + "\n" + notes,
		"min_chars": "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	spans := res["spans"].([]app.Span)
	if len(spans) != 3 || spans[1].Text != "First note about the tides" || spans[2].Source != "b.txt" {
		t.Errorf("spans %+v", spans)
	}
	if !strings.HasPrefix(res["listing"].(string), "[0] Kept the north light burning\n") {
		t.Errorf("listing %q", res["listing"])
	}
}

func TestSpansReadsOnePathPerLineSpacesIncluded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keeper notes")
	must(t, os.Mkdir(dir, 0o700))
	log := filepath.Join(dir, "night log.txt")
	must(t, os.WriteFile(log, []byte("Kept the north light burning\n"), 0o600))

	out, err := tools.NewTextSpans(1<<20).Invoke(context.Background(), map[string]string{
		"paths":     "\n  " + log + "  \n\n",
		"min_chars": "10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spans := out.(map[string]any)["spans"].([]app.Span); len(spans) != 1 || spans[0].Source != "night log.txt" {
		t.Errorf("spans %+v", spans)
	}
}

func TestSpansFailsOnAMissingPathAndOnNoSpans(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.txt")
	must(t, os.WriteFile(short, []byte("ok\n"), 0o600))
	cases := map[string]map[string]string{
		"missing":   {"paths": filepath.Join(dir, "absent.txt"), "min_chars": "5"},
		"no spans":  {"paths": short, "min_chars": "5"},
		"no paths":  {"paths": "", "min_chars": "5"},
		"bad count": {"paths": short, "min_chars": "many"},
	}
	for name, with := range cases {
		if _, err := tools.NewTextSpans(1<<20).Invoke(context.Background(), with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

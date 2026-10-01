package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
)

// docGetTool is an in-memory docs.get over reg.records, keyed by path.
type docGetTool struct{ reg *fakeRegistry }

func (t docGetTool) Name() string { return "docs.get" }

func (t docGetTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	t.reg.mu.Lock()
	r, ok := t.reg.records[with["path"]]
	t.reg.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("docs.get: %s: not found", with["path"])
	}
	out := map[string]any{"path": with["path"], "text": r.body}
	var doc any
	if json.Unmarshal([]byte(r.body), &doc) == nil {
		out["doc"] = doc
	}
	return out, nil
}

// documentClient serves the shelf view over reg and returns a client and
// its Origin. The view content is irrelevant to Document; it reuses the
// shared fixture.
func documentClient(t *testing.T, cfg web.Config, reg *fakeRegistry) (uiv1.UIServiceClient, string) {
	t.Helper()
	return serveView(t, cfg, shelfView, reg, web.Deps{})
}

func TestDocumentServesARecordPath(t *testing.T) {
	reg := newFakeRegistry(t, nil)
	reg.mu.Lock()
	reg.records = map[string]record{
		"logbook/day-1.json": {kind: "logbook", body: `{"wind":"west"}`},
		"notes/a.txt":        {kind: "note", body: "plain text"},
	}
	reg.mu.Unlock()
	c, origin := documentClient(t, runConfig(), reg)

	resp, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "record", Path: "logbook/day-1.json"}, origin))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if string(resp.Msg.Body) != `{"wind":"west"}` || resp.Msg.MediaType != "application/json" || resp.Msg.Name != "day-1.json" {
		t.Errorf("json record = %+v", resp.Msg)
	}

	resp2, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "record", Path: "notes/a.txt"}, origin))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if string(resp2.Msg.Body) != "plain text" || resp2.Msg.MediaType != "text/plain; charset=utf-8" || resp2.Msg.Name != "a.txt" {
		t.Errorf("plain record = %+v", resp2.Msg)
	}

	if _, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "bogus", Path: "x"}, origin)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("unknown source: err = %v, want InvalidArgument", err)
	}
}

func TestDocumentServesAFileUnderTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.4 fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := runConfig()
	cfg.FilesRoot = root
	reg := newFakeRegistry(t, nil)
	c, origin := documentClient(t, cfg, reg)

	resp, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "file", Path: "report.pdf"}, origin))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if string(resp.Msg.Body) != "%PDF-1.4 fake" || resp.Msg.MediaType != "application/pdf" || resp.Msg.Name != "report.pdf" {
		t.Errorf("file = %+v", resp.Msg)
	}
}

func TestDocumentFileNeedsARoot(t *testing.T) {
	reg := newFakeRegistry(t, nil)
	c, origin := documentClient(t, runConfig(), reg) // no FilesRoot

	_, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "file", Path: "x.txt"}, origin))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("err = %v, want FailedPrecondition", err)
	}
}

func TestDocumentRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	type escapeCase struct{ name, path string }
	cases := []escapeCase{
		{"dot dot", "../x"},
		{"absolute", "/etc/passwd"},
		{"directory", "sub"},
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, escapeCase{"symlink escaping the root", "escape.txt"})
	}

	cfg := runConfig()
	cfg.FilesRoot = root
	reg := newFakeRegistry(t, nil)
	c, origin := documentClient(t, cfg, reg)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Document(context.Background(), withOrigin(&uiv1.DocumentRequest{Source: "file", Path: tc.path}, origin))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

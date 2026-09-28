package web

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/domain"
)

// maxDocumentFileBytes bounds a file served from the files root.
const maxDocumentFileBytes = 32 << 20

// Document serves one document as a download: a record path read through
// docs.get, or a file under the configured files root. The browser saves
// the body; nothing is rendered inline.
func (s *Server) Document(ctx context.Context, req *connect.Request[uiv1.DocumentRequest]) (*connect.Response[uiv1.DocumentResponse], error) {
	switch req.Msg.Source {
	case "record":
		return s.recordDocument(ctx, req.Msg.Path)
	case "file":
		return s.fileDocument(req.Msg.Path)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("web: unknown document source %q", req.Msg.Source))
	}
}

// recordDocument reads path through a one-step docs.get blueprint, taking
// the run slot like any other internal blueprint, and returns its text as
// the body: application/json when the body parses as JSON, text/plain
// otherwise.
func (s *Server) recordDocument(ctx context.Context, path string) (*connect.Response[uiv1.DocumentResponse], error) {
	bp := domain.Blueprint{
		Name: "web.document",
		Vars: map[string]string{"path": path},
		Steps: []domain.Step{{ID: "get", Tool: "docs.get", With: map[string]string{
			"path": "{{.vars.path}}",
		}}},
	}
	state, err := s.runInternal(ctx, bp)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnknown, err)
	}
	out, _ := state.Outputs()["get"].(map[string]any)
	text, _ := out["text"].(string)
	mediaType := "text/plain; charset=utf-8"
	if _, ok := out["doc"]; ok {
		mediaType = "application/json"
	}
	return connect.NewResponse(&uiv1.DocumentResponse{
		Body:      []byte(text),
		MediaType: mediaType,
		Name:      filepath.Base(path),
	}), nil
}

// fileDocument reads path from the configured files root through os.Root,
// which refuses "..", absolute paths and symlinks that escape the root.
func (s *Server) fileDocument(path string) (*connect.Response[uiv1.DocumentResponse], error) {
	if s.cfg.FilesRoot == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("web: no files root configured"))
	}
	root, err := os.OpenRoot(s.cfg.FilesRoot)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("web: %w", err))
	}
	defer root.Close()

	f, err := root.Open(path)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("web: %w", err))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("web: %w", err))
	}
	if !info.Mode().IsRegular() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("web: %q is not a regular file", path))
	}
	if info.Size() > maxDocumentFileBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("web: %q is %d bytes, over the %d-byte bound", path, info.Size(), maxDocumentFileBytes))
	}

	body, err := io.ReadAll(f)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("web: %w", err))
	}

	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	return connect.NewResponse(&uiv1.DocumentResponse{
		Body:      body,
		MediaType: mediaType,
		Name:      filepath.Base(path),
	}), nil
}

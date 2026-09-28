// Package web is the inbound HTTP adapter for the web UI: session token
// exchange, request guards, the embedded static build and the UIService RPC.
// This is the security boundary in front of a real person's record
// (docs/specs/2026-09-27-epic-13-web-ui.md, "What is never exposed").
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/app"
)

// Endpoint is one endpoint the registry sends data to: the tools that send
// there, and whether it is off this machine.
type Endpoint struct {
	Endpoint string
	Hosted   bool
	Tools    []string
}

// Config bounds a run's execution time and names the files root downloads
// are served from. An empty FilesRoot turns file serving off.
type Config struct {
	RunTimeout time.Duration
	FilesRoot  string
}

// Deps are the server's collaborators: the loaded views, the loader used to
// validate a Run's target pack, the runner that executes it, and the egress
// table Views reports.
type Deps struct {
	Views  []View
	Load   Loader
	Runner *app.Runner
	Asker  *Asker
	Egress []Endpoint
	Server map[string]string // store_root, files_root
}

// Server is the UI's Connect handler and its guarded static file server.
type Server struct {
	uiv1.UnimplementedUIServiceHandler
	cfg     Config
	deps    Deps
	handler http.Handler
	slot    chan struct{} // holds a token while a run executes
	pending atomic.Int32  // runs waiting for or holding the slot
	cache   stateCache
	acks    acks
}

// New builds the UI server. token authenticates the browser; host is the
// exact "ip:port" the listener is bound to.
func New(cfg Config, deps Deps, token, host string) (*Server, error) {
	static, err := newStaticHandler()
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, deps: deps, slot: make(chan struct{}, 1)}

	mux := http.NewServeMux()
	rpcPrefix, rpcHandler := uiv1.NewUIServiceHandler(s)
	mux.Handle(rpcPrefix, rpcHandler)
	mux.Handle("/", static)

	s.handler = guard(host, token, rpcPrefix, mux)
	return s, nil
}

// Handler is the server's guarded top-level http.Handler.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// NewToken is a fresh startup token: 32 bytes from crypto/rand, hex-encoded.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("web: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Views reports the loaded view files, the egress table with each hosted
// endpoint's acknowledgement state, and whether file downloads are enabled.
func (s *Server) Views(ctx context.Context, _ *connect.Request[uiv1.ViewsRequest]) (*connect.Response[uiv1.ViewsResponse], error) {
	viewsJSON, err := json.Marshal(s.deps.Views)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	pending, err := s.unacknowledged(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	egress := make([]*uiv1.Endpoint, len(s.deps.Egress))
	for i, e := range s.deps.Egress {
		egress[i] = &uiv1.Endpoint{
			Endpoint: e.Endpoint,
			Hosted:   e.Hosted,
			Tools:    e.Tools,
			Acknowledged: e.Hosted && !slices.ContainsFunc(pending, func(p Endpoint) bool {
				return p.Endpoint == e.Endpoint
			}),
		}
	}

	return connect.NewResponse(&uiv1.ViewsResponse{
		ViewsJson:    string(viewsJSON),
		Egress:       egress,
		FilesEnabled: s.cfg.FilesRoot != "",
	}), nil
}

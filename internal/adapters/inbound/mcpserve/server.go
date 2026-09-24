// Package mcpserve offers a chosen set of Atlas's registry tools to an agent
// as an MCP server over HTTP. The agent drives it, so it is an inbound
// adapter. Every call is put to ports.Permission before the tool runs.
package mcpserve

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// ServerName is the name the agent knows this server by.
const ServerName = "atlas"

// Kind is the permission kind of every call to an Atlas tool.
const Kind = "atlas"

const (
	endpoint = "/mcp"
	version  = "0"
)

// inputSchema accepts any object of scalars: a ports.Tool takes strings,
// and flatten renders numbers and booleans as strings.
var inputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": map[string]any{"type": []string{"string", "number", "boolean"}},
}

// Config names the registry tools to offer, bounds what goes back to the
// agent, and carries the token every request must present.
type Config struct {
	Tools          []string
	MaxResultBytes int
	SummaryBytes   int
	Token          string
}

// NewHandler builds the MCP endpoint over reg. A configured tool the
// registry lacks, or two tools that publish under the same name, fail here.
func NewHandler(reg ports.Registry, perm ports.Permission, cfg Config) (http.Handler, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, nil)
	published := map[string]string{}
	for _, name := range cfg.Tools {
		tool, ok := reg.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("mcpserve: no tool named %q to offer the agent", name)
		}
		pub := PublishedName(name)
		if other, taken := published[pub]; taken {
			return nil, fmt.Errorf("mcpserve: %q and %q both publish as %q", other, name, pub)
		}
		published[pub] = name
		server.AddTool(&mcp.Tool{
			Name:        pub,
			Description: fmt.Sprintf("Atlas tool %s. Pass its arguments as top-level string, number or boolean fields.", name),
			InputSchema: inputSchema,
		}, handle(tool, perm, cfg))
	}

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.Handle(endpoint, requireToken(cfg.Token, h))
	return mux, nil
}

// PublishedName is a registry name as the agent sees it. Model APIs limit
// tool names to letters, digits, underscore and hyphen.
func PublishedName(registryName string) string {
	return strings.ReplaceAll(registryName, ".", "_")
}

func handle(tool ports.Tool, perm ports.Permission, cfg Config) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		with, err := flatten(args)
		if err != nil {
			return failed("%s: %v", tool.Name(), err), nil
		}
		d, err := perm.Decide(ctx, ports.PermissionRequest{
			ToolName: tool.Name(),
			Kind:     Kind,
			Summary:  app.BoundSummary(tool.Name()+" "+string(args), cfg.SummaryBytes),
		})
		if err != nil || d != ports.PermissionAllow {
			return failed("permission denied for %s", tool.Name()), nil
		}
		out, err := tool.Invoke(ctx, with)
		if err != nil {
			return failed("%v", err), nil
		}
		text, err := render(out)
		if err != nil {
			return failed("%s: %v", tool.Name(), err), nil
		}
		if len(text) > cfg.MaxResultBytes {
			return failed("result of %s exceeds %d bytes", tool.Name(), cfg.MaxResultBytes), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// render is a tool result as text: a string as itself, anything else as
// JSON.
func render(out any) (string, error) {
	if s, ok := out.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(b), nil
}

// failed is a tool error the agent sees and can correct, not a protocol
// error.
func failed(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewToken is a fresh secret for one run.
func NewToken() string { return rand.Text() }

// AuthHeader is the header the agent must send.
func AuthHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// Listen serves h on addr and returns the server and its MCP endpoint URL.
// The serving goroutine is owned by the returned server and stops at its
// Shutdown or Close.
func Listen(addr string, h http.Handler, headerTimeout time.Duration) (*http.Server, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("mcpserve: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: headerTimeout}
	go func() { _ = srv.Serve(ln) }() // owned by srv, stops at srv.Shutdown or srv.Close
	return srv, "http://" + ln.Addr().String() + endpoint, nil
}

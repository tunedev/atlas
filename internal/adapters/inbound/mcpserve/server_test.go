package mcpserve_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"
	"github.com/tunedev/atlas/internal/core/ports"
)

type echoTool struct{ got map[string]string }

func (e *echoTool) Name() string { return "weather.get" }
func (e *echoTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	e.got = with
	return map[string]any{"city": with["city"], "temp_c": 11}, nil
}

type registry map[string]ports.Tool

func (r registry) Lookup(name string) (ports.Tool, bool) { t, ok := r[name]; return t, ok }

type fixed struct {
	d     ports.PermissionDecision
	asked []ports.PermissionRequest
}

func (f *fixed) Decide(_ context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	f.asked = append(f.asked, req)
	return f.d, nil
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func serve(t *testing.T, perm ports.Permission, maxResult int) (*mcp.ClientSession, *echoTool) {
	t.Helper()
	tool := &echoTool{}
	token := mcpserve.NewToken()
	h, err := mcpserve.NewHandler(registry{"weather.get": tool, "hidden.tool": tool}, perm,
		mcpserve.Config{Tools: []string{"weather.get"}, MaxResultBytes: maxResult, SummaryBytes: 200, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, tool
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestOnlyConfiguredToolsArePublished(t *testing.T) {
	session, _ := serve(t, &fixed{d: ports.PermissionAllow}, 1<<20)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "weather_get" {
		t.Errorf("tools %+v; want only weather_get", res.Tools)
	}
}

func TestAgentCallsARegistryToolByItsPublishedName(t *testing.T) {
	perm := &fixed{d: ports.PermissionAllow}
	session, tool := serve(t, perm, 1<<20)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo", "days": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || text(res) != `{"city":"Oslo","temp_c":11}` {
		t.Errorf("result %q (error %v)", text(res), res.IsError)
	}
	if tool.got["days"] != "2" {
		t.Errorf("tool received %v", tool.got)
	}
	if len(perm.asked) != 1 || perm.asked[0].ToolName != "weather.get" || perm.asked[0].Kind != mcpserve.Kind {
		t.Errorf("permission asked %+v; want one request for weather.get of kind %s", perm.asked, mcpserve.Kind)
	}
}

func TestDeniedCallDoesNotRun(t *testing.T) {
	session, tool := serve(t, &fixed{d: ports.PermissionDeny}, 1<<20)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text(res), "denied") || tool.got != nil {
		t.Errorf("result %q error=%v; tool ran with %v", text(res), res.IsError, tool.got)
	}
}

func TestBadArgumentsAndOversizeResultsAreToolErrors(t *testing.T) {
	session, _ := serve(t, &fixed{d: ports.PermissionAllow}, 10)
	res, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"where": map[string]any{"city": "Oslo"}}})
	if !res.IsError || !strings.Contains(text(res), `"where"`) {
		t.Errorf("nested: %q", text(res))
	}
	res, _ = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	if !res.IsError || !strings.Contains(text(res), "10 bytes") {
		t.Errorf("oversize: %q", text(res))
	}
}

func TestRequestsWithoutTheTokenAreRefused(t *testing.T) {
	h, _ := mcpserve.NewHandler(registry{}, &fixed{}, mcpserve.Config{Token: mcpserve.NewToken()})
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d; want 401", resp.StatusCode)
	}
}

func TestConfigErrorsFailAtConstruction(t *testing.T) {
	tool := &echoTool{}
	if _, err := mcpserve.NewHandler(registry{}, &fixed{}, mcpserve.Config{Tools: []string{"missing.tool"}}); err == nil || !strings.Contains(err.Error(), "missing.tool") {
		t.Errorf("missing tool: err = %v", err)
	}
	clash := registry{"a.b": tool, "a_b": tool}
	if _, err := mcpserve.NewHandler(clash, &fixed{}, mcpserve.Config{Tools: []string{"a.b", "a_b"}}); err == nil || !strings.Contains(err.Error(), "a_b") {
		t.Errorf("clash: err = %v", err)
	}
}

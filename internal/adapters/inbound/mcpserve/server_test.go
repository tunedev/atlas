package mcpserve_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"
	"github.com/tunedev/atlas/internal/core/ports"
)

// testCallTimeout bounds tool calls in tests that are not themselves
// exercising mcpserve.Config.CallTimeout. It is generous relative to any
// test's own client-side deadline, so it never fires first, and distinct
// from blocking's 5s fallback so the two cannot be mistaken for each other.
const testCallTimeout = 3 * time.Second

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
	return serveWithCallTimeout(t, perm, maxResult, testCallTimeout)
}

func serveWithCallTimeout(t *testing.T, perm ports.Permission, maxResult int, callTimeout time.Duration) (*mcp.ClientSession, *echoTool) {
	t.Helper()
	tool := &echoTool{}
	token := mcpserve.NewToken()
	h, err := mcpserve.NewHandler(registry{"weather.get": tool, "hidden.tool": tool}, perm,
		mcpserve.Config{Tools: []string{"weather.get"}, MaxResultBytes: maxResult, SummaryBytes: 200, Token: token, CallTimeout: callTimeout})
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
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"where": map[string]any{"city": "Oslo"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text(res), `"where"`) {
		t.Errorf("nested: %q", text(res))
	}
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	if err != nil {
		t.Fatal(err)
	}
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

func TestWrongOrMalformedTokenIsRefused(t *testing.T) {
	token := mcpserve.NewToken()
	h, err := mcpserve.NewHandler(registry{}, &fixed{}, mcpserve.Config{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	cases := map[string]string{
		"wrong token":            "Bearer wrong",
		"right token, no prefix": token,
	}
	for name, header := range cases {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d; want 401", name, resp.StatusCode)
		}
	}
}

// blocking is a Permission whose Decide blocks until ctx is done or a long
// fallback elapses, reporting which happened first. It stands in for a
// human prompt that would otherwise hang forever on an abandoned call.
type blocking struct{ cancelled chan error }

func (b *blocking) Decide(ctx context.Context, _ ports.PermissionRequest) (ports.PermissionDecision, error) {
	select {
	case <-ctx.Done():
		b.cancelled <- ctx.Err()
		return ports.PermissionDeny, ctx.Err()
	case <-time.After(5 * time.Second):
		b.cancelled <- nil
		return ports.PermissionDeny, nil
	}
}

// waitForCancellation fails the test if Decide does not observe cancellation
// within a window well short of blocking's 5s fallback, proving the call was
// bounded rather than left to run to that fallback.
func waitForCancellation(t *testing.T, cancelled chan error) {
	t.Helper()
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("Decide returned via its fallback, not cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Decide did not observe cancellation within 2s")
	}
}

func TestACancelledCallCancelsItsPermissionPrompt(t *testing.T) {
	perm := &blocking{cancelled: make(chan error, 1)}
	// A CallTimeout well past the assertion window: isolates
	// PropagateRequestCancellation as the only mechanism that can cancel
	// the still-pending permission prompt within that window.
	session, _ := serveWithCallTimeout(t, perm, 1<<20, testCallTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	waitForCancellation(t, perm.cancelled)
}

func TestACallIsBoundedByCallTimeout(t *testing.T) {
	perm := &blocking{cancelled: make(chan error, 1)}
	session, _ := serveWithCallTimeout(t, perm, 1<<20, 200*time.Millisecond)
	// No client-side deadline: isolates CallTimeout as the only mechanism
	// that can cancel the still-pending permission prompt.
	_, _ = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	waitForCancellation(t, perm.cancelled)
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

// callRaw posts body to a server whose permission engine is perm and
// returns what perm was asked. It bypasses the SDK client, which rewrites
// arguments before they reach the wire.
func callRaw(t *testing.T, perm *fixed, body string) []ports.PermissionRequest {
	t.Helper()
	token := mcpserve.NewToken()
	h, err := mcpserve.NewHandler(registry{"weather.get": &echoTool{}}, perm,
		mcpserve.Config{Tools: []string{"weather.get"}, MaxResultBytes: 1 << 20, SummaryBytes: 200, Token: token, CallTimeout: testCallTimeout})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Transport: bearer{token}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(perm.asked) != 1 {
		t.Fatalf("permission asked %d times; want 1 (status %s, reply %s)", len(perm.asked), resp.Status, reply)
	}
	return perm.asked
}

func TestPermissionSummaryIsOneCompactLine(t *testing.T) {
	body := "{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"tools/call\",\n" +
		" \"params\": {\"name\": \"weather_get\", \"arguments\": {\n    \"city\":  \"Oslo\",\r\n\t\"days\": 2\n  }}}"
	asked := callRaw(t, &fixed{d: ports.PermissionDeny}, body)
	if got, want := asked[0].Summary, `weather.get {"city":"Oslo","days":2}`; got != want {
		t.Errorf("summary %q; want %q", got, want)
	}
}

func TestACallWithoutArgumentsIsAsked(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"weather_get"}}`
	asked := callRaw(t, &fixed{d: ports.PermissionDeny}, body)
	if got := asked[0].Summary; !strings.HasPrefix(got, "weather.get") {
		t.Errorf("summary %q; want one naming weather.get", got)
	}
}

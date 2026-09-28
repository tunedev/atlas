package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/app"
)

// newTestServer starts a guarded web.Server on 127.0.0.1:0 and returns it
// alongside the bound host and startup token.
func newTestServer(t *testing.T, deps web.Deps, cfg web.Config) (*httptest.Server, string, string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.NotFoundHandler())
	host := ts.Listener.Addr().String()
	token := web.NewToken()

	srv, err := web.New(cfg, deps, token, host)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	return ts, host, token
}

// exchangeSession posts token to /session with origin as its Origin header
// and fails the test unless the exchange succeeds.
func exchangeSession(t *testing.T, client *http.Client, baseURL, origin, token string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/session", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("session exchange status %d; want 204", resp.StatusCode)
	}
}

// authedClient returns an http.Client holding a valid session cookie for
// host, plus that host's Origin value.
func authedClient(t *testing.T, baseURL, host, token string) (*http.Client, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	origin := "http://" + host
	exchangeSession(t, client, baseURL, origin, token)
	return client, origin
}

// withOrigin returns a connect request carrying the given Origin header.
func withOrigin[T any](msg *T, origin string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Origin", origin)
	return req
}

func TestRejectsForeignHost(t *testing.T) {
	ts, _, _ := newTestServer(t, web.Deps{}, web.Config{})

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example:7878"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("status %d; want %d", resp.StatusCode, http.StatusMisdirectedRequest)
	}
}

func TestRejectsForeignOrigin(t *testing.T) {
	ts, host, token := newTestServer(t, web.Deps{}, web.Config{})
	client, _ := authedClient(t, ts.URL, host, token)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/atlas.web.v1.UIService/Views", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %d; want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestRejectsMissingSession(t *testing.T) {
	ts, host, _ := newTestServer(t, web.Deps{}, web.Config{})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := uiv1.NewUIServiceClient(&http.Client{Jar: jar}, ts.URL)

	_, err = client.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, "http://"+host))
	if err == nil {
		t.Fatal("want an error for a request with no session cookie")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Errorf("code = %v; want %v", code, connect.CodeUnauthenticated)
	}
}

func TestSessionExchangeSetsAStrictHttpOnlyCookie(t *testing.T) {
	ts, host, token := newTestServer(t, web.Deps{}, web.Config{})
	origin := "http://" + host
	client := &http.Client{}

	wrongBody, _ := json.Marshal(map[string]string{"token": "wrong"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/session", bytes.NewReader(wrongBody))
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d; want 401", resp.StatusCode)
	}

	rightBody, _ := json.Marshal(map[string]string{"token": token})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/session", bytes.NewReader(rightBody))
	req.Header.Set("Origin", origin)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("right token: status %d; want 204", resp.StatusCode)
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "atlas_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no atlas_session cookie set")
	}
	if !cookie.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v; want Strict", cookie.SameSite)
	}
	if cookie.Value != token {
		t.Errorf("cookie value %q; want %q", cookie.Value, token)
	}
}

func TestAnOversizedSessionBodyDoesNotAuthenticate(t *testing.T) {
	ts, host, token := newTestServer(t, web.Deps{}, web.Config{})
	body, _ := json.Marshal(map[string]string{"token": token, "pad": strings.Repeat("x", 2048)})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/session", bytes.NewReader(body))
	req.Header.Set("Origin", "http://"+host)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode > 499 {
		t.Errorf("status %d; want 4xx", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("an oversized body set cookies %v", resp.Cookies())
	}
}

func TestNewRefusesAnEmptyToken(t *testing.T) {
	if _, err := web.New(web.Config{}, web.Deps{}, "", "127.0.0.1:1"); err == nil || err.Error() != "web: empty token" {
		t.Errorf("New with an empty token: err = %v; want web: empty token", err)
	}
}

func TestEveryResponseCarriesTheSecurityHeadersAndNoCORS(t *testing.T) {
	ts, host, token := newTestServer(t, web.Deps{}, web.Config{})
	origin := "http://" + host

	checkHeaders := func(t *testing.T, h http.Header) {
		t.Helper()
		if got := h.Get("Content-Security-Policy"); got != "default-src 'self'; frame-ancestors 'none'" {
			t.Errorf("CSP = %q", got)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q", got)
		}
		if got := h.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q", got)
		}
		for k := range h {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("unexpected CORS header %s", k)
			}
		}
	}

	// A static request.
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	checkHeaders(t, resp.Header)

	// A successful RPC.
	client, _ := authedClient(t, ts.URL, host, token)
	uiClient := uiv1.NewUIServiceClient(client, ts.URL)
	rpcResp, err := uiClient.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatal(err)
	}
	checkHeaders(t, rpcResp.Header())

	// A 401: no session cookie.
	jar, _ := cookiejar.New(nil)
	anon := uiv1.NewUIServiceClient(&http.Client{Jar: jar}, ts.URL)
	_, err = anon.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err == nil {
		t.Fatal("want an error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("err %v is not a *connect.Error", err)
	}
	checkHeaders(t, connectErr.Meta())
}

func TestViewsReturnsTheLoadedViews(t *testing.T) {
	views := []web.View{{
		ID:        "shelf",
		Title:     "Shelf",
		Discloses: []string{"model"},
		Screens: []web.Screen{{
			ID:    "home",
			Title: "Home",
			Show:  []web.Widget{{Kind: "text", From: "state.msg"}},
		}},
	}}
	egress := []web.Endpoint{{Endpoint: "https://api.example", Hosted: true, Tools: []string{"shelf.scan"}}}

	deps := web.Deps{Views: views, Egress: egress, Runner: app.NewRunner(newFakeRegistry(t, nil))}
	ts, host, token := newTestServer(t, deps, web.Config{FilesRoot: "/data", RunTimeout: time.Second})
	client, origin := authedClient(t, ts.URL, host, token)
	uiClient := uiv1.NewUIServiceClient(client, ts.URL)

	resp, err := uiClient.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatal(err)
	}

	var gotViews []web.View
	if err := json.Unmarshal([]byte(resp.Msg.ViewsJson), &gotViews); err != nil {
		t.Fatalf("unmarshal views_json: %v", err)
	}
	if len(gotViews) != 1 || gotViews[0].ID != "shelf" || gotViews[0].Title != "Shelf" {
		t.Errorf("views %+v", gotViews)
	}
	if len(gotViews[0].Screens) != 1 || gotViews[0].Screens[0].ID != "home" {
		t.Errorf("screens %+v", gotViews[0].Screens)
	}

	if len(resp.Msg.Egress) != 1 {
		t.Fatalf("egress %+v", resp.Msg.Egress)
	}
	got := resp.Msg.Egress[0]
	if got.Endpoint != "https://api.example" || !got.Hosted || got.Acknowledged {
		t.Errorf("egress[0] = %+v", got)
	}

	if !resp.Msg.FilesEnabled {
		t.Error("files_enabled = false; want true")
	}
}

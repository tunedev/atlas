package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestHTTPFetchesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"first"}]}`))
	}))
	defer srv.Close()

	out, err := tools.NewHTTP(5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("out is %T, want map", out)
	}
	if _, ok := m["items"]; !ok {
		t.Errorf("out = %#v", m)
	}
}

func TestHTTPReturnsNonJSONAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain body"))
	}))
	defer srv.Close()

	out, err := tools.NewHTTP(5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("out is %T, want map", out)
	}
	if m["body"] != "plain body" {
		t.Errorf("out = %#v; a non-JSON body should be reachable as .body", m)
	}
}

func TestHTTPFailsWithoutAURL(t *testing.T) {
	if _, err := tools.NewHTTP(time.Second, 1<<20).Invoke(context.Background(), map[string]string{}); err == nil {
		t.Error("Invoke succeeded with no url")
	}
}

func TestHTTPFailsOnUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := tools.NewHTTP(5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"url": srv.URL}); err == nil {
		t.Error("Invoke succeeded on a 503")
	}
}

func TestHTTPName(t *testing.T) {
	if got := tools.NewHTTP(time.Second, 1<<20).Name(); got != "http.request" {
		t.Errorf("Name = %q", got)
	}
}

func TestHTTPFailsWhenBodyExceedsMaxBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	if _, err := tools.NewHTTP(5*time.Second, 9).Invoke(context.Background(),
		map[string]string{"url": srv.URL}); err == nil {
		t.Error("Invoke succeeded with a body larger than maxBytes")
	}
}

func TestHTTPReportsTheStatusOnAnUpstreamErrorWithAnOverLimitBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	_, err := tools.NewHTTP(5*time.Second, 9).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err == nil {
		t.Fatal("Invoke succeeded on a 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the 500 status; the over-limit body must not mask it", err)
	}
}

func TestHTTPSucceedsWhenBodyIsExactlyAtMaxBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	out, err := tools.NewHTTP(5*time.Second, 10).Invoke(context.Background(),
		map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["body"] != "0123456789" {
		t.Errorf("out = %#v; a body exactly at maxBytes should still succeed", out)
	}
}

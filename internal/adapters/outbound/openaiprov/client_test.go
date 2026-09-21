package openaiprov_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/core/ports"
)

// recorded is a real response shape, trimmed, as returned by an
// OpenAI-compatible endpoint when top_logprobs is requested.
const recorded = `{
 "model": "a-model",
 "usage": {"prompt_tokens": 11, "completion_tokens": 2},
 "choices": [{
   "message": {"content": "{\"answer\": \"yes\"}"},
   "logprobs": {"content": [
     {"token": "yes", "logprob": -1.3244,
      "top_logprobs": [
        {"token": "Yes", "logprob": -0.3975},
        {"token": "yes", "logprob": -1.3244},
        {"token": "no",  "logprob": -4.3892}]}
   ]}
 }]
}`

func serve(t *testing.T, status int, body string, capture *http.Request) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			*capture = *r.Clone(r.Context())
			b, _ := io.ReadAll(r.Body)
			capture.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func client(t *testing.T, base string) *openaiprov.Client {
	t.Helper()
	return openaiprov.New(openaiprov.Config{
		Name: "test", BaseURL: base, Model: "a-model",
		Timeout: 5 * time.Second, MaxBytes: 1 << 20,
	})
}

func TestCompletionCarriesTextModelAndUsage(t *testing.T) {
	s := serve(t, http.StatusOK, recorded, nil)
	got, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(got.Text, `"yes"`) {
		t.Errorf("text = %q", got.Text)
	}
	if got.Model != "a-model" {
		t.Errorf("model = %q, want a-model", got.Model)
	}
	if got.Usage.PromptTokens != 11 || got.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", got.Usage)
	}
	if got.Latency <= 0 {
		t.Error("latency was not measured")
	}
}

func TestAlternativesSurviveTheBoundary(t *testing.T) {
	s := serve(t, http.StatusOK, recorded, nil)
	got, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", TopLogProbs: 3})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(got.Tokens) != 1 {
		t.Fatalf("tokens = %d, want 1", len(got.Tokens))
	}
	tok := got.Tokens[0]
	if tok.Text != "yes" {
		t.Errorf("token text = %q", tok.Text)
	}
	if len(tok.Alternatives) != 3 {
		t.Fatalf("alternatives = %d, want 3", len(tok.Alternatives))
	}
	// The chosen token is not the most probable one. That is the whole reason
	// alternatives cross the boundary.
	if math.Exp(tok.Alternatives[0].LogProb) <= math.Exp(tok.LogProb) {
		t.Errorf("expected a more probable alternative than the chosen token: %+v", tok)
	}
}

func TestTopLogProbsIsOnlyRequestedWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		n    int
	}{{"absent", false, 0}, {"present", true, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			var captured http.Request
			s := serve(t, http.StatusOK, recorded, &captured)
			_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", TopLogProbs: tc.n})
			if err != nil {
				t.Fatalf("complete: %v", err)
			}
			body, _ := io.ReadAll(captured.Body)
			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}
			_, has := sent["top_logprobs"]
			if has != tc.want {
				t.Errorf("top_logprobs present = %v, want %v; request was %s", has, tc.want, body)
			}
		})
	}
}

func TestASchemaIsSentWhenSet(t *testing.T) {
	var captured http.Request
	s := serve(t, http.StatusOK, recorded, &captured)
	schema := []byte(`{"type":"object","properties":{"answer":{"type":"string"}}}`)
	_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", Schema: schema})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	body, _ := io.ReadAll(captured.Body)
	if !strings.Contains(string(body), "json_schema") {
		t.Errorf("schema was not sent: %s", body)
	}
}

func TestANonSuccessStatusIsAnErrorNamingIt(t *testing.T) {
	s := serve(t, http.StatusInternalServerError, `{"error":"boom"}`, nil)
	_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"})
	if err == nil {
		t.Fatal("a 500 returned no error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error does not name the status: %v", err)
	}
	if !strings.Contains(err.Error(), "openaiprov: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
}

func TestAnOverLongBodyIsRefusedRatherThanTruncated(t *testing.T) {
	big := `{"model":"a-model","choices":[{"message":{"content":"` + strings.Repeat("x", 4096) + `"}}]}`
	s := serve(t, http.StatusOK, big, nil)
	c := openaiprov.New(openaiprov.Config{
		Name: "test", BaseURL: s.URL, Model: "a-model",
		Timeout: 5 * time.Second, MaxBytes: 256,
	})
	if _, err := c.Complete(context.Background(), ports.Prompt{User: "q"}); err == nil {
		t.Fatal("an over-limit body was accepted")
	}
}

func TestNoChoicesIsAnErrorRatherThanAnEmptyCompletion(t *testing.T) {
	s := serve(t, http.StatusOK, `{"model":"a-model","choices":[]}`, nil)
	if _, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"}); err == nil {
		t.Fatal("an empty choices array produced no error")
	}
}

// TestAgainstALiveEngine is skipped unless ATLAS_LIVE_PROVIDER is set, so CI
// never depends on a running engine. The base URL and model come from
// ATLAS_LIVE_BASE_URL and ATLAS_LIVE_MODEL, defaulting to a local Ollama.
func TestAgainstALiveEngine(t *testing.T) {
	if os.Getenv("ATLAS_LIVE_PROVIDER") == "" {
		t.Skip("ATLAS_LIVE_PROVIDER not set")
	}
	baseURL := os.Getenv("ATLAS_LIVE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	model := os.Getenv("ATLAS_LIVE_MODEL")
	if model == "" {
		model = "qwen2.5-coder:7b"
	}
	c := openaiprov.New(openaiprov.Config{
		Name: "live", BaseURL: baseURL, Model: model,
		Timeout: 3 * time.Minute, MaxBytes: 10 << 20,
	})
	got, err := c.Complete(context.Background(), ports.Prompt{
		User: "Reply with the single word: yes", MaxTokens: 5, TopLogProbs: 3,
	})
	if err != nil {
		t.Fatalf("live complete: %v", err)
	}
	if len(got.Tokens) == 0 {
		t.Fatal("a live engine returned no per-token data when asked for it")
	}
	t.Logf("live: text=%q model=%q latency=%s tokens=%d", got.Text, got.Model, got.Latency, len(got.Tokens))
}

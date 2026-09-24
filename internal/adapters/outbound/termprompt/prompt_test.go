package termprompt_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/termprompt"
	"github.com/tunedev/atlas/internal/core/ports"
)

// syncBuffer is a bytes.Buffer safe to write from one goroutine and read
// from another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

var req = ports.PermissionRequest{ToolName: "notes.write", Kind: "edit", Summary: "Write notes.txt"}

func TestAnswers(t *testing.T) {
	cases := map[string]ports.PermissionDecision{
		"y\n": ports.PermissionAllow, "YES\n": ports.PermissionAllow, " yes \n": ports.PermissionAllow,
		"n\n": ports.PermissionDeny, "\n": ports.PermissionDeny, "maybe\n": ports.PermissionDeny,
		"": ports.PermissionDeny, // EOF
	}
	for in, want := range cases {
		out := &syncBuffer{}
		got, err := termprompt.New(strings.NewReader(in), out).Decide(context.Background(), req)
		if err != nil || got != want {
			t.Errorf("answer %q: Decide = %q, %v; want %q", in, got, err, want)
		}
		for _, part := range []string{req.ToolName, req.Kind, req.Summary} {
			if !strings.Contains(out.String(), part) {
				t.Errorf("prompt %q does not show %q", out.String(), part)
			}
		}
	}
}

func TestCancelledContextDenies(t *testing.T) {
	in, _ := io.Pipe() // never answers
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got, err := termprompt.New(in, &syncBuffer{}).Decide(ctx, req)
	if got != ports.PermissionDeny || err == nil {
		t.Errorf("Decide = %q, %v; want deny with the context's error", got, err)
	}
}

func TestConcurrentPromptsDoNotInterleave(t *testing.T) {
	in, answer := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)

	a := ports.PermissionRequest{ToolName: "a.tool", Kind: "read", Summary: "A"}
	b := ports.PermissionRequest{ToolName: "b.tool", Kind: "read", Summary: "B"}
	got := map[string]ports.PermissionDecision{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range []ports.PermissionRequest{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _ := p.Decide(context.Background(), r)
			mu.Lock()
			got[r.ToolName] = d
			mu.Unlock()
		}()
	}

	// Answer yes to whichever prompt shows first, no to the second.
	first := waitForPrompt(t, out, 1)
	_, _ = io.WriteString(answer, "y\n")
	waitForPrompt(t, out, 2)
	_, _ = io.WriteString(answer, "n\n")
	wg.Wait()

	second := "b.tool"
	if first == "b.tool" {
		second = "a.tool"
	}
	if got[first] != ports.PermissionAllow || got[second] != ports.PermissionDeny {
		t.Errorf("decisions %v; want %s allowed and %s denied", got, first, second)
	}
}

// waitForPrompt waits until out holds n prompts and returns the tool name
// in the most recent one.
func waitForPrompt(t *testing.T, out *syncBuffer, n int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if strings.Count(s, "allow? [y/N]") == n {
			last := s[strings.LastIndex(s, "run ")+len("run "):]
			return last[:strings.Index(last, " ")]
		}
		if strings.Count(s, "allow? [y/N]") > n {
			t.Fatalf("a second prompt appeared before the first was answered:\n%s", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("prompt %d never appeared:\n%s", n, out.String())
	return ""
}

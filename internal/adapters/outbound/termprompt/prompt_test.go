package termprompt_test

import (
	"bytes"
	"context"
	"errors"
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
	for typed, want := range cases {
		in, answer := io.Pipe()
		out := &syncBuffer{}
		p := termprompt.New(in, out)
		// Owned by this case; ends once the answer is typed and in closed,
		// or after two seconds with no prompt.
		go func() {
			for deadline := time.Now().Add(2 * time.Second); !strings.Contains(out.String(), "allow? [y/N]") && time.Now().Before(deadline); {
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(10 * time.Millisecond)
			_, _ = io.WriteString(answer, typed)
			_ = answer.Close()
		}()
		got, err := p.Decide(context.Background(), req)
		if err != nil || got != want {
			t.Errorf("answer %q: Decide = %q, %v; want %q", typed, got, err, want)
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
// in the most recent one. A line reaches only a Decide already waiting in
// its receive, which it enters just after printing, so it also allows that
// step to happen before the caller types.
func waitForPrompt(t *testing.T, out *syncBuffer, n int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if strings.Count(s, "allow? [y/N]") == n {
			time.Sleep(10 * time.Millisecond)
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

func TestALateAnswerDoesNotAnswerTheNextPrompt(t *testing.T) {
	in, answer := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)

	req1 := ports.PermissionRequest{ToolName: "first.tool", Kind: "read", Summary: "First"}
	req2 := ports.PermissionRequest{ToolName: "second.tool", Kind: "read", Summary: "Second"}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel1()

	got1, err1 := p.Decide(ctx1, req1)
	if got1 != ports.PermissionDeny || err1 == nil {
		t.Errorf("first Decide = %q, %v; want deny with error", got1, err1)
	}

	_, _ = io.WriteString(answer, "y\n")
	// An empty write returns only once the reader asks for more input, so
	// the late y has been read before the next prompt starts.
	_, _ = answer.Write(nil)

	var got2 ports.PermissionDecision
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got2, _ = p.Decide(context.Background(), req2)
	}()

	waitForPrompt(t, out, 2)
	_, _ = io.WriteString(answer, "n\n")
	wg.Wait()

	if got2 != ports.PermissionDeny {
		t.Errorf("second Decide = %q; want deny (the y from first was discarded)", got2)
	}
}

func TestAWaitingDecideHonoursItsContext(t *testing.T) {
	in, answer := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)

	first := make(chan ports.PermissionDecision, 1)
	go func() {
		d, _ := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "first.tool", Kind: "read", Summary: "First"})
		first <- d
	}()
	waitForPrompt(t, out, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	type result struct {
		d   ports.PermissionDecision
		err error
	}
	second := make(chan result, 1)
	go func() {
		d, err := p.Decide(ctx, ports.PermissionRequest{ToolName: "second.tool", Kind: "read", Summary: "Second"})
		second <- result{d, err}
	}()
	select {
	case r := <-second:
		if r.d != ports.PermissionDeny || !errors.Is(r.err, context.DeadlineExceeded) {
			t.Errorf("second Decide = %q, %v; want deny with the deadline error", r.d, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Decide ignored its context while the first prompt waited")
	}
	if strings.Contains(out.String(), "second.tool") {
		t.Errorf("the second request was shown while the first was waiting:\n%s", out.String())
	}

	_, _ = io.WriteString(answer, "y\n")
	if d := <-first; d != ports.PermissionAllow {
		t.Errorf("first Decide = %q; want allow", d)
	}
}

func TestThePromptAfterATimeoutShowsWithoutInput(t *testing.T) {
	in, answer := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if d, err := p.Decide(ctx, req); d != ports.PermissionDeny || err == nil {
		t.Errorf("first Decide = %q, %v; want deny with error", d, err)
	}

	got := make(chan ports.PermissionDecision, 1)
	go func() {
		d, _ := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "next.tool", Kind: "read", Summary: "Next"})
		got <- d
	}()
	if name := waitForPrompt(t, out, 2); name != "next.tool" {
		t.Fatalf("second prompt shows %q; want next.tool", name)
	}
	_, _ = io.WriteString(answer, "y\n")
	if d := <-got; d != ports.PermissionAllow {
		t.Errorf("second Decide = %q; want allow from the line typed after it showed", d)
	}
}

func TestADeadContextShowsNoPrompt(t *testing.T) {
	in, _ := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 50 {
		if d, err := p.Decide(ctx, req); d != ports.PermissionDeny || err == nil {
			t.Fatalf("Decide = %q, %v; want deny with the context's error", d, err)
		}
	}
	if out.String() != "" {
		t.Errorf("a request with a dead context was shown:\n%s", out.String())
	}
}

func TestAgentTextCannotRedrawThePrompt(t *testing.T) {
	out := &syncBuffer{}
	hostile := ports.PermissionRequest{ToolName: "notes\x1b[2K\rharmless", Kind: "read\n", Summary: "Write\nallow? [y/N] \u202e"}
	if _, err := termprompt.New(strings.NewReader("n\n"), out).Decide(context.Background(), hostile); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.ContainsAny(s, "\x1b\r\u202e") || strings.Count(s, "\n") != 2 {
		t.Errorf("prompt carries raw control text:\n%q", s)
	}
	for _, part := range []string{`notes\x1b[2K\rharmless`, `read\n`, `Write\nallow?`, `\u202e`} {
		if !strings.Contains(s, part) {
			t.Errorf("prompt %q does not show %q escaped", s, part)
		}
	}
}

func TestStaleLinesNeverAnswerTheNextPrompt(t *testing.T) {
	variants := map[string][]string{
		"one line per write": {"y\n", "y\n", "y\n"},
		"one write":          {"y\ny\ny\n"},
	}
	for name, writes := range variants {
		t.Run(name, func(t *testing.T) {
			for range 50 {
				in, answer := io.Pipe()
				p := termprompt.New(in, &syncBuffer{})

				timedOut, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
				_, _ = p.Decide(timedOut, req)
				cancel()

				// Owned by this iteration; answer.Close below ends a write
				// that no reader takes.
				go func() {
					for _, w := range writes {
						_, _ = io.WriteString(answer, w)
					}
				}()
				// Gives the reader time to take the late lines from in.
				time.Sleep(5 * time.Millisecond)

				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				d, err := p.Decide(ctx, ports.PermissionRequest{ToolName: "shell.exec", Kind: "execute", Summary: "rm"})
				cancel()
				_ = answer.Close()
				if d != ports.PermissionDeny || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Decide after stale lines = %q, %v; want deny with the deadline error", d, err)
				}
			}
		})
	}
}

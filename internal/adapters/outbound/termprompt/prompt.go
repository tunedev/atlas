// Package termprompt asks a human on the terminal whether a tool call may
// run.
package termprompt

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Prompt shows one request at a time on out and reads the answer from in.
// Only y or yes allows; any other answer, end of input, or a cancelled
// context denies.
type Prompt struct {
	out  io.Writer
	asks chan chan string
	mu   sync.Mutex
}

// New starts the reader goroutine that owns in. It stops when in reaches
// end of input.
func New(in io.Reader, out io.Writer) *Prompt {
	p := &Prompt{out: out, asks: make(chan chan string)}
	go p.read(in) // owns in, stops at EOF
	return p
}

func (p *Prompt) read(in io.Reader) {
	s := bufio.NewScanner(in)
	for reply := range p.asks {
		if !s.Scan() {
			close(reply)
			continue
		}
		reply <- s.Text()
	}
}

func (p *Prompt) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	reply := make(chan string, 1)
	select {
	case p.asks <- reply:
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", ctx.Err())
	}

	fmt.Fprintf(p.out, "atlas: the agent wants to run %s (%s)\n  %s\nallow? [y/N] ", req.ToolName, req.Kind, req.Summary)
	select {
	case line, ok := <-reply:
		if ok && isYes(line) {
			return ports.PermissionAllow, nil
		}
		return ports.PermissionDeny, nil
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", ctx.Err())
	}
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

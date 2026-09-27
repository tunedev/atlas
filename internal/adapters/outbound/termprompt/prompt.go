// Package termprompt asks a human on the terminal whether a tool call may
// run.
package termprompt

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Prompt shows one request at a time on out and reads the answer from in.
// Only y or yes allows; any other answer, end of input, or a cancelled
// context denies. A line typed while no prompt is showing answers nothing.
type Prompt struct {
	out   io.Writer
	lines <-chan string
	slot  chan struct{}
}

// New starts the reader goroutine that owns in. A line reaches only a
// Decide already waiting for it after printing its prompt; any other line
// is dropped. At end of input or a read error the reader closes the line
// channel, and every later Decide denies.
func New(in io.Reader, out io.Writer) *Prompt {
	lines := make(chan string)
	// Owned by the Prompt; stops at end of input or a read error, and
	// otherwise lives as long as in does.
	go read(in, lines)
	return &Prompt{out: out, lines: lines, slot: make(chan struct{}, 1)}
}

func read(in io.Reader, lines chan<- string) {
	s := bufio.NewScanner(in)
	for s.Scan() {
		select {
		case lines <- s.Text():
		default:
		}
	}
	close(lines)
}

func (p *Prompt) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	select {
	case p.slot <- struct{}{}:
	case <-ctx.Done():
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", ctx.Err())
	}
	defer func() { <-p.slot }()
	if err := ctx.Err(); err != nil {
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", err)
	}

	fmt.Fprintf(p.out, "atlas: the agent wants to run %s (%s)\n  %s\nallow? [y/N] ",
		graphic(req.ToolName), graphic(req.Kind), graphic(req.Summary))
	select {
	case line, ok := <-p.lines:
		if ok && isYes(line) {
			return ports.PermissionAllow, nil
		}
		return ports.PermissionDeny, nil
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", ctx.Err())
	}
}

// graphic escapes every non-printable rune in s, so text from the agent
// cannot move the cursor, clear a line or start a new one.
func graphic(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

// Package typstconv converts Typst source to PDF with the typst binary
// already on the machine. It never installs one.
package typstconv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config says which binary to run and how to bound it.
type Config struct {
	Bin      string
	Timeout  time.Duration
	MaxBytes int64
}

// Converter runs `typst compile` from stdin to stdout with an empty
// directory as its root, so a template reads no file and fetches nothing.
type Converter struct {
	bin string
	cfg Config
}

// New finds cfg.Bin, failing when it is not installed.
func New(cfg Config) (*Converter, error) {
	bin, err := exec.LookPath(cfg.Bin)
	if err != nil {
		return nil, fmt.Errorf("typstconv: %s not found: %w", cfg.Bin, err)
	}
	return &Converter{bin: bin, cfg: cfg}, nil
}

func (c *Converter) Pair() ports.Pair { return ports.Pair{From: "typst", To: "pdf"} }

func (c *Converter) Convert(ctx context.Context, dst io.Writer, src io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	root, err := os.MkdirTemp("", "typst-root-")
	if err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	defer os.RemoveAll(root)

	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.bin, "compile", "--root", root, "-", "-")
	cmd.Stdin = src
	cmd.Stdout = &limitedWriter{w: &out, left: c.cfg.MaxBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, left: c.cfg.MaxBytes}
	cmd.WaitDelay = c.cfg.Timeout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("typstconv: %w", ctx.Err())
		}
		return fmt.Errorf("typstconv: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if _, err := dst.Write(out.Bytes()); err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	return nil
}

// limitedWriter accepts at most left bytes and then fails, so an oversize
// document stops the process rather than filling memory.
type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, fmt.Errorf("output exceeds the limit")
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

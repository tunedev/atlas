// Package typstconv converts Typst source to PDF with the typst binary
// already on the machine. It never installs one.
//
// A source document reads no file and fetches nothing over the network.
// An empty --root stops file reads, but not a package import: typst
// resolves "@preview/..." imports through its own package cache and, when
// one is missing, downloads it regardless of --root. Convert closes that
// gap too: it points --package-path and --package-cache-path at empty
// directories, and it replaces every proxy environment variable with one
// pointing at a dead address, so any download attempt is refused before a
// byte reaches the network.
package typstconv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// deadProxy is where every proxy environment variable is pointed during a
// conversion. Port 9 is the discard port; nothing listens there, so any
// download attempt gets an immediate connection refusal instead of a real
// network round trip.
const deadProxy = "http://127.0.0.1:9"

// Converter runs `typst compile` from stdin to stdout with an empty root,
// an empty package path, an empty package cache path, and a dead proxy in
// its environment, so a template reads no file, imports no package, and
// fetches nothing over the network.
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

	parent, err := os.MkdirTemp("", "typst-")
	if err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	defer os.RemoveAll(parent)
	root := filepath.Join(parent, "root")
	pkgs := filepath.Join(parent, "pkgs")
	cache := filepath.Join(parent, "cache")
	for _, dir := range []string{root, pkgs, cache} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return fmt.Errorf("typstconv: %w", err)
		}
	}

	var out, stderr bytes.Buffer
	stdout := &limitedWriter{w: &out, left: c.cfg.MaxBytes}
	errW := &limitedWriter{w: &stderr, left: c.cfg.MaxBytes}
	cmd := exec.CommandContext(ctx, c.bin, "compile",
		"--root", root,
		"--package-path", pkgs,
		"--package-cache-path", cache,
		"-", "-")
	cmd.Stdin = src
	cmd.Stdout = stdout
	cmd.Stderr = errW
	cmd.WaitDelay = c.cfg.Timeout
	cmd.Env = withDeadProxy(os.Environ())
	if err := cmd.Run(); err != nil {
		if stdout.err != nil {
			return fmt.Errorf("typstconv: output exceeds %d bytes: %w", c.cfg.MaxBytes, stdout.err)
		}
		if errW.err != nil {
			return fmt.Errorf("typstconv: output exceeds %d bytes: %w", c.cfg.MaxBytes, errW.err)
		}
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			return fmt.Errorf("typstconv: %s: %w", msg, ctx.Err())
		}
		return fmt.Errorf("typstconv: %s: %w", msg, err)
	}
	if _, err := dst.Write(out.Bytes()); err != nil {
		return fmt.Errorf("typstconv: %w", err)
	}
	return nil
}

// withDeadProxy returns env with every proxy variable (any case, including
// NO_PROXY) removed and HTTPS_PROXY, HTTP_PROXY and ALL_PROXY added back
// pointing at deadProxy, so the child cannot reach the network through a
// proxy the caller's own environment configured.
func withDeadProxy(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		key, _, found := strings.Cut(kv, "=")
		if found && isProxyVar(key) {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"HTTPS_PROXY="+deadProxy,
		"HTTP_PROXY="+deadProxy,
		"ALL_PROXY="+deadProxy,
	)
}

func isProxyVar(key string) bool {
	return strings.HasSuffix(strings.ToLower(key), "_proxy")
}

// errTooLarge marks a limitedWriter that refused a write for exceeding its
// budget, so Convert can report the real cause instead of the broken pipe
// the child sees when its next write finds the pipe already closed.
var errTooLarge = errors.New("output exceeds the limit")

// limitedWriter accepts at most left bytes and then fails, so an oversize
// document stops the process rather than filling memory.
type limitedWriter struct {
	w    io.Writer
	left int64
	err  error
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		l.err = errTooLarge
		return 0, l.err
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

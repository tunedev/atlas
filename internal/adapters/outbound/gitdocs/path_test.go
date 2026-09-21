package gitdocs

import (
	"strings"
	"testing"
)

// TestSafeRelPathIsSlashCanonical guards the property the Windows CI
// failures exposed: a git tree path is always slash-separated, on every
// platform, so safeRelPath must validate and return slash form regardless
// of the host OS. Unlike a test that only exercises Put/Get through the
// public API, this passes or fails identically on every platform because it
// never touches path/filepath, whose behaviour follows the host OS.
func TestSafeRelPathIsSlashCanonical(t *testing.T) {
	accepted := []struct {
		in   string
		want string
	}{
		{"notes/one.md", "notes/one.md"},
		{"a/./one.md", "a/one.md"},
		{"a/b/../one.md", "a/one.md"},
	}
	for _, c := range accepted {
		got, err := safeRelPath(c.in)
		if err != nil {
			t.Errorf("safeRelPath(%q) returned error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("safeRelPath(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, `\`) {
			t.Errorf("safeRelPath(%q) = %q contains a backslash", c.in, got)
		}
	}

	rejected := []string{
		"",
		"/etc/passwd",
		"../escape.md",
		"a/../../escape.md",
		`notes\one.md`,
		`..\escape.md`,
	}
	for _, in := range rejected {
		if got, err := safeRelPath(in); err == nil {
			t.Errorf("safeRelPath(%q) = %q, nil, want an error", in, got)
		}
	}
}

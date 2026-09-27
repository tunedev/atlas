// Package pidlock keeps one process at a time working in a directory. A lock
// is a file holding its owner's pid. A lock whose owner is no longer running,
// whose owner is this process's own pid, or whose content is not a pid, is
// stale and is taken over rather than left for a person to delete.
package pidlock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Lock is a lock this process holds.
type Lock struct {
	path      string
	pid       int
	reclaimed int
}

// Reclaimed returns the pid held by the stale lock Acquire took over, or 0
// when there was none or its content was not a pid.
func (l *Lock) Reclaimed() int { return l.reclaimed }

// Acquire takes the lock at path for this process. It fails, naming the pid,
// when another running process holds it. A stale lock is removed and taken;
// a lock holding this process's own pid is stale, since a process restarted
// under the same pid (a container's pid 1) is what leaves one.
func Acquire(path string) (*Lock, error) {
	return acquire(path, os.Getpid())
}

// acquire takes the lock at path on behalf of pid.
func acquire(path string, pid int) (*Lock, error) {
	reclaimed := 0
	for range 2 {
		err := create(path, pid)
		if err == nil {
			return &Lock{path: path, pid: pid, reclaimed: reclaimed}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("pidlock: %w", err)
		}
		holder, ok := readPid(path)
		if ok && holder != pid && alive(holder) {
			return nil, fmt.Errorf("pidlock: %s is held by running process %d; if %d is not atlas, delete %s", path, holder, holder, path)
		}
		reclaimed = 0
		if ok {
			reclaimed = holder
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("pidlock: remove stale lock %s: %w", path, err)
		}
	}
	return nil, fmt.Errorf("pidlock: %s was taken while it was being reclaimed", path)
}

// Release removes the lock if it still holds this process's pid. A lock
// another process has since taken is left alone.
func (l *Lock) Release() error {
	if holder, ok := readPid(l.path); !ok || holder != l.pid {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("pidlock: release %s: %w", l.path, err)
	}
	return nil
}

// create puts a lock holding pid at path, failing if path already exists.
// The pid is written to a temp file first and then linked into place, so a
// lock is never visible without its pid.
func create(path string, pid int) error {
	tmp, err := writeTemp(filepath.Dir(path), filepath.Base(path), pid)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	return os.Link(tmp, path)
}

// writeTemp writes pid to a new temp file in dir and returns its path.
func writeTemp(dir, base string, pid int) (string, error) {
	f, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// readPid reads the pid a lock holds. It reports false when the file is
// missing or holds anything but a positive integer.
func readPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}

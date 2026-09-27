// Package pidlock keeps one process at a time working in a directory. A lock
// is a file holding its owner's pid. A lock whose owner is no longer running,
// or whose content is not a pid, is stale and is taken over rather than left
// for a person to delete.
package pidlock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Lock is a lock this process holds.
type Lock struct {
	path string
	pid  int
}

// Acquire takes the lock at path for this process. It fails, naming the pid,
// when a running process holds it, including this one. A stale lock is
// removed and taken.
func Acquire(path string) (*Lock, error) {
	pid := os.Getpid()
	for range 2 {
		err := create(path, pid)
		if err == nil {
			return &Lock{path: path, pid: pid}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("pidlock: %w", err)
		}
		if holder, ok := readPid(path); ok && alive(holder) {
			return nil, fmt.Errorf("pidlock: %s is held by running process %d", path, holder)
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

// create writes pid to path, failing if path already exists.
func create(path string, pid int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
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

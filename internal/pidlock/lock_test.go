package pidlock_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/pidlock"
)

func lockPath(t *testing.T) string { return filepath.Join(t.TempDir(), ".atlas.lock") }

func holder(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestAcquireWritesThePidAndReleaseRemovesIt(t *testing.T) {
	path := lockPath(t)
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := holder(t, path); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock holds %q, want this process's pid", got)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock still present after Release: %v", err)
	}
}

func TestALiveHolderIsRefusedByPid(t *testing.T) {
	path := lockPath(t)
	live := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pidlock.Acquire(path)
	if err == nil || !strings.Contains(err.Error(), live) {
		t.Fatalf("err = %v, want a refusal naming pid %s", err, live)
	}
	if got := holder(t, path); got != live {
		t.Errorf("a refused Acquire changed the lock to %q", got)
	}
}

func TestADeadHoldersLockIsReclaimed(t *testing.T) {
	// A process that has already exited: this test binary, run with no tests.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper: %v", err)
	}
	dead := strconv.Itoa(cmd.ProcessState.Pid())
	path := lockPath(t)
	if err := os.WriteFile(path, []byte(dead+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over a dead holder (pid %s): %v", dead, err)
	}
	defer func() { _ = l.Release() }()
	if got := holder(t, path); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("reclaimed lock holds %q, want this process's pid", got)
	}
}

func TestAGarbledLockIsReclaimed(t *testing.T) {
	path := lockPath(t)
	if err := os.WriteFile(path, []byte("not a pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over a garbled lock: %v", err)
	}
	_ = l.Release()
}

func TestReleaseLeavesALockThatIsNoLongerOurs(t *testing.T) {
	path := lockPath(t)
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := holder(t, path); got != "1" {
		t.Errorf("Release removed a lock another process holds; it now reads %q", got)
	}
}

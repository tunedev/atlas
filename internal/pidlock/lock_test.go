package pidlock_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	if n := l.Reclaimed(); n != 0 {
		t.Errorf("Reclaimed() = %d on a fresh lock, want 0", n)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("directory holds %d entries after Acquire, want the lock alone", len(entries))
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock still present after Release: %v", err)
	}
}

// blockEnv makes TestHelperBlock block, so a re-run of this test binary
// stands in for another live process.
const blockEnv = "PIDLOCK_HELPER_BLOCK"

func TestHelperBlock(t *testing.T) {
	if os.Getenv(blockEnv) != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(time.Minute)
}

// liveOtherPid starts a process that stays running until the test ends.
func liveOtherPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperBlock$")
	cmd.Env = append(os.Environ(), blockEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestALiveHolderIsRefusedByPid(t *testing.T) {
	path := lockPath(t)
	live := strconv.Itoa(liveOtherPid(t))
	if err := os.WriteFile(path, []byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pidlock.Acquire(path)
	want := fmt.Sprintf("pidlock: %s is held by running process %s; if %s is not atlas, delete %s", path, live, live, path)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := holder(t, path); got != live {
		t.Errorf("a refused Acquire changed the lock to %q", got)
	}
}

// A restarted container runs as the same pid it had before, so a lock
// holding this process's own pid was left by an earlier life of it.
func TestALockHoldingOurOwnPidIsReclaimed(t *testing.T) {
	path := lockPath(t)
	own := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(own+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := pidlock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over a lock holding our own pid: %v", err)
	}
	_ = l.Release()
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
	if got := strconv.Itoa(l.Reclaimed()); got != dead {
		t.Errorf("Reclaimed() = %s, want the dead holder's pid %s", got, dead)
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

// Racers stand in for separate processes, each under the pid of its own live
// helper, all taking one lock at once: exactly one may hold it.
func TestRacingAcquiresHaveExactlyOneWinner(t *testing.T) {
	const racers, trials = 4, 200
	pids := make([]int, racers)
	for i := range pids {
		pids[i] = liveOtherPid(t)
	}
	path := lockPath(t)
	for trial := range trials {
		if n := raceOnce(path, pids); n != 1 {
			t.Fatalf("trial %d: %d racers hold the lock, want exactly 1", trial, n)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

// raceOnce has every pid acquire path at the same moment and returns how many
// succeeded.
func raceOnce(path string, pids []int) int {
	var won atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, pid := range pids {
		wg.Go(func() {
			<-start
			if _, err := pidlock.AcquireAs(path, pid); err == nil {
				won.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	return int(won.Load())
}

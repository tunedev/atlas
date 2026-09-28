//go:build unix

package pidlock

import (
	"os"
	"testing"
)

// Pid 1 is always running on unix and, for an ordinary user, cannot be
// signalled: kill(1, 0) returns EPERM, which must read as alive.
func TestAliveTreatsPermissionDeniedAsAlive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: kill(1, 0) succeeds, so EPERM is not exercised")
	}
	if !alive(1) {
		t.Error("alive(1) = false; a process owned by another user must read as alive")
	}
}

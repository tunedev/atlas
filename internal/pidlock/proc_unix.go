//go:build unix

package pidlock

import (
	"errors"
	"syscall"
)

// alive reports whether pid is a running process. Signal 0 checks for the
// process without delivering anything; EPERM means it exists under another
// user.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

//go:build windows

package pidlock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited.
const stillActive = 259

// alive reports whether pid is a running process. Access denied means it
// exists under another user.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

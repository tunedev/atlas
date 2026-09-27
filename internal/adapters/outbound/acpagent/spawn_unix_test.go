//go:build unix

package acpagent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnEscapedChild starts a grandchild in its own session, detached from
// this process's process group, so a process-group kill cannot reach it.
// It inherits this process's stderr, holding that pipe open, and prints its
// pid to stderr so a test can find and kill it afterward.
func spawnEscapedChild() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ACPAGENT_STUB=stubborn")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return
	}
	fmt.Fprintln(os.Stderr, "escaped-child-pid", cmd.Process.Pid)
}

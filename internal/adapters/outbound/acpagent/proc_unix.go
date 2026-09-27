//go:build unix

package acpagent

import (
	"os/exec"
	"syscall"
)

// isolate starts cmd in its own process group, so killTree can reach every
// descendant that inherited its stdio, not just the direct child.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills cmd's whole process group.
func killTree(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

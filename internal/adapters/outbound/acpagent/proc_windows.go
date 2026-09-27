//go:build windows

package acpagent

import "os/exec"

// isolate does nothing on windows: process groups are a unix mechanism, so
// killTree here reaches only the direct child.
func isolate(*exec.Cmd) {}

// killTree kills cmd's direct process.
func killTree(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

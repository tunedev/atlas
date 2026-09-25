//go:build windows

package acpagent

// spawnEscapedChild is a no-op on windows: process sessions are a unix
// mechanism, and the test that exercises this stub mode is skipped there.
func spawnEscapedChild() {}

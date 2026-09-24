package acpagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestMain makes the test binary double as a minimal ACP agent when
// ACPAGENT_STUB names a mode, so launch and shutdown are tested against a
// real subprocess.
//
//	clean     answers initialize, exits at stdin EOF
//	stubborn  answers initialize, ignores stdin EOF
//	die       answers initialize, exits on the next request without replying
//	orphan    answers initialize, starts a grandchild that inherits stdout
//	          and stderr and ignores stdin EOF, then itself ignores stdin EOF
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACPAGENT_STUB"); mode != "" {
		runStub(mode)
		return
	}
	os.Exit(m.Run())
}

func runStub(mode string) {
	fmt.Fprintln(os.Stderr, "stub ready")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var m message
		_ = json.Unmarshal(in.Bytes(), &m)
		if m.Method == "initialize" {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID,
				"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}})
			fmt.Println(string(b))
			if mode == "orphan" {
				spawnOrphanChild()
			}
			continue
		}
		if mode == "die" {
			os.Exit(3)
		}
	}
	if mode == "stubborn" || mode == "orphan" {
		time.Sleep(time.Hour)
	}
}

// spawnOrphanChild starts a grandchild stub that inherits this process's
// stdout and stderr and ignores stdin EOF. It is not waited on: killing
// only this process leaves the grandchild running and holding those
// descriptors open.
func spawnOrphanChild() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ACPAGENT_STUB=stubborn")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Start()
}

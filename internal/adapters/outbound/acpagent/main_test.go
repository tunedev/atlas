package acpagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
			continue
		}
		if mode == "die" {
			os.Exit(3)
		}
	}
	if mode == "stubborn" {
		time.Sleep(time.Hour)
	}
}

package tools_test

import (
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestRegistryLooksUpByToolName(t *testing.T) {
	r := tools.NewRegistry(tools.NewHTTP(time.Second, 1<<20), tools.NewModel("http://x", "m", time.Second, 1<<20))

	if _, ok := r.Lookup("http.request"); !ok {
		t.Error("http.request not registered")
	}
	if _, ok := r.Lookup("model.complete"); !ok {
		t.Error("model.complete not registered")
	}
	if _, ok := r.Lookup("absent"); ok {
		t.Error("Lookup found a tool that was never registered")
	}
}

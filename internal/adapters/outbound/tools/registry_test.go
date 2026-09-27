package tools_test

import (
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func TestRegistryLooksUpByToolName(t *testing.T) {
	r := tools.NewRegistry(tools.NewHTTP(time.Second, 1<<20), tools.NewModel(stubProvider{}))

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

func TestWithAddsWithoutChangingTheOriginal(t *testing.T) {
	base := tools.NewRegistry(tools.NewHTTP(time.Second, 1))
	extended := base.With(tools.NewModel(nil))
	if _, ok := extended.Lookup("model.complete"); !ok {
		t.Error("With did not add the tool")
	}
	if _, ok := base.Lookup("model.complete"); ok {
		t.Error("With changed the registry it was called on")
	}
}

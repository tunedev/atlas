package main

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/inbound/packfile"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
)

// sendablePack is the pack whose render steps are checked, and nothingKept
// and oneKept are claims.settle inputs with no supported statement and with
// one supported statement in every section.
const (
	sendablePack = "packs/tailor.yaml"
	nothingKept  = `{"requirements":[],"bullets":[],"letter":{"sentences":[],"answers":[]}}`
	oneKept      = `{"requirements":[],
	 "bullets":[{"citations":[{"id":0,"quote":"Logged every passing ship by name","status":"grounded"}]}],
	 "letter":{"sentences":[{"text":"I kept the lamp lit through every gale.","citations":[{"id":1,"quote":"Kept the lamp lit","status":"grounded","relevant":true}]}],"answers":[]}}`
)

// TestASendableDocumentWithNothingKeptIsNotRendered renders every render
// step of the pack that verifies kept content, bound the way the runner binds
// it, from claims.settle's real output: with nothing kept the render fails
// and writes no file, and with one kept statement it renders.
func TestASendableDocumentWithNothingKeptIsNotRendered(t *testing.T) {
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst is not on PATH")
	}
	t.Chdir(filepath.Join("..", ".."))
	b, err := packfile.Load(sendablePack)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		fields  string
		wantErr bool
	}{
		"nothing kept": {nothingKept, true},
		"one kept":     {oneKept, false},
	} {
		state := settledState(t, b, tc.fields)
		checked := 0
		for _, s := range b.Steps {
			if s.Tool != "render.run" || !strings.Contains(s.With["expect"], ".steps.settle.kept") {
				continue
			}
			checked++
			out, err := renderStep(t, s, state)
			if (err != nil) != tc.wantErr {
				t.Errorf("%s: step %s: err = %v; want error %v", name, s.ID, err, tc.wantErr)
			}
			if _, statErr := os.Stat(out); tc.wantErr && !os.IsNotExist(statErr) {
				t.Errorf("%s: step %s wrote %s with nothing verified in it", name, s.ID, out)
			}
		}
		if checked == 0 {
			t.Fatalf("%s has no render step that verifies kept content", sendablePack)
		}
	}
}

// settledState is the state the pack's render steps see: its vars with a
// fresh out_dir, an empty history, and claims.settle's output for fields.
func settledState(t *testing.T, b domain.Blueprint, fields string) *domain.State {
	t.Helper()
	vars := maps.Clone(b.Vars)
	vars["out_dir"] = t.TempDir()
	state := domain.NewState(vars)
	settled, err := tools.NewClaimsSettle().Invoke(context.Background(), map[string]string{"fields": fields})
	if err != nil {
		t.Fatal(err)
	}
	state.Put("history", map[string]any{"body": `{"entries":[]}`})
	state.Put("settle", settled)
	return state
}

// renderStep renders s's config against state and runs render.run with it,
// returning the output path and render.run's error.
func renderStep(t *testing.T, s domain.Step, state *domain.State) (string, error) {
	t.Helper()
	with := make(map[string]string, len(s.With))
	for k, raw := range s.With {
		v, err := app.Render(raw, state)
		if err != nil {
			t.Fatalf("step %s: %v", s.ID, err)
		}
		with[k] = v
	}
	conv, err := typstconv.New(typstconv.Config{Bin: "typst", Timeout: time.Minute, MaxBytes: 1 << 24})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools.NewRender(conv, 1<<24).Invoke(context.Background(), with)
	return with["output"], err
}

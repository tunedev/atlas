package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type scriptedHuman struct {
	answer ports.PermissionDecision
	err    error
	asked  []ports.PermissionRequest
}

func (h *scriptedHuman) Decide(_ context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	h.asked = append(h.asked, req)
	return h.answer, h.err
}

func TestPolicyFirstMatchingRuleWins(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionAllow}
	p := app.NewPermissionPolicy([]app.PermissionRule{
		{ToolName: "*", Kind: "delete", Decision: ports.PermissionDeny},
		{ToolName: "notes.write", Kind: "*", Decision: ports.PermissionAllow},
		{ToolName: "*", Kind: "*", Decision: ports.PermissionDeny},
	}, human)

	cases := []struct {
		req  ports.PermissionRequest
		want ports.PermissionDecision
	}{
		{ports.PermissionRequest{ToolName: "notes.write", Kind: "delete"}, ports.PermissionDeny},
		{ports.PermissionRequest{ToolName: "notes.write", Kind: "edit"}, ports.PermissionAllow},
		{ports.PermissionRequest{ToolName: "weather.get", Kind: "fetch"}, ports.PermissionDeny},
	}
	for _, c := range cases {
		got, err := p.Decide(context.Background(), c.req)
		if err != nil || got != c.want {
			t.Errorf("Decide(%+v) = %q, %v; want %q", c.req, got, err, c.want)
		}
	}
	if len(human.asked) != 0 {
		t.Errorf("a rule decided every case, yet the human was asked %d times", len(human.asked))
	}
}

func TestPolicyAsksTheHumanWhenNoRuleMatches(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionAllow}
	p := app.NewPermissionPolicy(nil, human)
	req := ports.PermissionRequest{ToolName: "weather.get", Kind: "fetch", Summary: "Oslo"}

	got, err := p.Decide(context.Background(), req)
	if err != nil || got != ports.PermissionAllow {
		t.Fatalf("Decide = %q, %v; want allow", got, err)
	}
	if len(human.asked) != 1 || human.asked[0] != req {
		t.Errorf("human asked %+v; want exactly %+v", human.asked, req)
	}
}

func TestPolicyAsksTheHumanForAnAskRule(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionDeny}
	p := app.NewPermissionPolicy([]app.PermissionRule{{ToolName: "*", Kind: "execute", Decision: ports.PermissionAsk}}, human)

	got, _ := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "shell", Kind: "execute"})
	if got != ports.PermissionDeny || len(human.asked) != 1 {
		t.Errorf("Decide = %q after %d asks; want deny after 1", got, len(human.asked))
	}
}

func TestPolicyFailsClosed(t *testing.T) {
	cases := map[string]*scriptedHuman{
		"human error":    {answer: ports.PermissionAllow, err: errors.New("terminal gone")},
		"human says ask": {answer: ports.PermissionAsk},
	}
	for name, human := range cases {
		p := app.NewPermissionPolicy(nil, human)
		got, err := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "t", Kind: "k"})
		if got != ports.PermissionDeny {
			t.Errorf("%s: Decide = %q; want deny", name, got)
		}
		if (human.err != nil) != (err != nil) {
			t.Errorf("%s: err = %v; want the human's error surfaced, and only then", name, err)
		}
	}
}

func TestBoundSummary(t *testing.T) {
	cases := []struct {
		in     string
		budget int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"a longer summary", 8, "a longer..."},
		{"naïve", 3, "na..."}, // never splits the two-byte ï
	}
	for _, c := range cases {
		if got := app.BoundSummary(c.in, c.budget); got != c.want {
			t.Errorf("BoundSummary(%q, %d) = %q; want %q", c.in, c.budget, got, c.want)
		}
	}
}

package ports_test

import (
	"context"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// stubJudge exists to prove the port is implementable from outside the package
// with no adapter type in any signature.
type stubJudge struct{}

func (stubJudge) Ask(_ context.Context, subject string, qs []ports.Question) (ports.Judgement, error) {
	answers := make([]ports.Answer, 0, len(qs))
	for _, q := range qs {
		answers = append(answers, ports.Answer{
			ID:           q.ID,
			Kind:         q.Kind,
			Chosen:       "yes",
			Distribution: map[string]float64{"yes": 0.9, "no": 0.1},
		})
	}
	return ports.Judgement{Subject: subject, Model: "stub-model", When: time.Now(), Answers: answers}, nil
}

func TestAJudgeCanBeImplementedOutsideTheCore(t *testing.T) {
	var j ports.Judge = stubJudge{}

	got, err := j.Ask(context.Background(), "a short book", []ports.Question{
		{ID: "worth_reading", Kind: ports.KindNoul, Ask: "Is it worth reading?"},
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got.Model == "" {
		t.Error("Judgement does not record which model answered")
	}
	if len(got.Answers) != 1 || got.Answers[0].ID != "worth_reading" {
		t.Fatalf("answers are not keyed by question id: %+v", got.Answers)
	}
	if got.Answers[0].Distribution["yes"] == 0 {
		t.Error("Answer carries no distribution")
	}
}

func TestANoulKnowsItsOwnOptionsAndForms(t *testing.T) {
	if len(ports.NoulOptions()) != 2 {
		t.Fatalf("noul options = %v, want yes and no", ports.NoulOptions())
	}
	forms := ports.NoulForms()
	if len(forms["yes"]) < 2 || len(forms["no"]) < 2 {
		t.Errorf("noul forms do not cover surface variants: %+v", forms)
	}
	// A caller must not be able to mutate the shared defaults.
	forms["yes"] = nil
	if ports.NoulForms()["yes"] == nil {
		t.Error("NoulForms returns a shared map; a caller can empty it for everyone")
	}
}

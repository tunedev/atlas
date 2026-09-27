package app_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func rainPrediction() app.Prediction {
	return app.Prediction{QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry"}}
}

// resolved records a judgement with probability p and attaches state.
func resolved(t *testing.T, docs ports.Docs, index ports.Index, subject string, p float64, state string) {
	t.Helper()
	path := judged(t, docs, index, subject, p, day)
	if state == "" {
		return
	}
	if _, err := app.AttachOutcome(context.Background(), docs, index, path, app.Outcome{State: state, When: day}); err != nil {
		t.Fatalf("attach: %v", err)
	}
}

func TestCalibrateScoresResolvedJudgementsAndCountsTheRest(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range 25 {
		resolved(t, docs, index, fmt.Sprintf("wet-%d", i), 0.8, "wet")
	}
	for i := range 10 {
		resolved(t, docs, index, fmt.Sprintf("dry-%d", i), 0.8, "dry")
	}
	resolved(t, docs, index, "pending", 0.8, "")
	resolved(t, docs, index, "foggy", 0.8, "fog")

	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if c.N != 35 {
		t.Errorf("N = %d, want 35", c.N)
	}
	if c.Excluded.Pending != 1 || c.Excluded.Unclassified != 1 {
		t.Errorf("excluded = %+v", c.Excluded)
	}
	want := (25*0.04 + 10*0.64) / 35
	if c.Brier == nil || fmt.Sprintf("%.6f", *c.Brier) != fmt.Sprintf("%.6f", want) {
		t.Errorf("Brier = %v, want %v", c.Brier, want)
	}
	if r := c.Bins[8].ObservedRate; r == nil || fmt.Sprintf("%.4f", *r) != fmt.Sprintf("%.4f", 25.0/35) {
		t.Errorf("0.8 bin rate = %v, want 25/35", r)
	}
}

func TestInconclusiveAndUnclassifiedOutcomesAreCountedSeparately(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	resolved(t, docs, index, "foggy", 0.8, "fog")
	resolved(t, docs, index, "hailing", 0.8, "hail")
	p := rainPrediction()
	p.Inconclusive = []string{"fog"}

	c, err := app.Calibrate(ctx, docs, index, p, app.CalibrateOptions{})
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if c.N != 0 {
		t.Errorf("N = %d, want 0", c.N)
	}
	if c.Excluded.Inconclusive != 1 || c.Excluded.Unclassified != 1 {
		t.Errorf("excluded = %+v, want Inconclusive: 1, Unclassified: 1", c.Excluded)
	}
}

func TestAZeroCoverageAnswerIsExcludedEvenWhenItWasRight(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	j := forecast("blind", 1.0, day)
	j.Answers[0].Coverage = ports.Coverage{Represented: 0, Declared: 2}
	path, err := app.RecordJudgement(ctx, docs, index, "blind", rainQuestion(), j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
		t.Fatal(err)
	}
	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.N != 0 || c.Excluded.ZeroCoverage != 1 {
		t.Errorf("N = %d excluded = %+v; a probability that measured nothing is not scored", c.N, c.Excluded)
	}
}

func TestAMisspeltOptionExcludesAndCountsRatherThanScoringZero(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	resolved(t, docs, index, "one", 0.9, "wet")
	p := rainPrediction()
	p.Options = []string{"yse"}
	c, err := app.Calibrate(ctx, docs, index, p, app.CalibrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.N != 0 || c.Excluded.OptionMismatch != 1 {
		t.Errorf("N = %d excluded = %+v", c.N, c.Excluded)
	}
}

func TestAPredictionThatCannotMeanAnythingIsRefused(t *testing.T) {
	docs, index := record(t)
	for name, p := range map[string]app.Prediction{
		"no question":        {Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry"}},
		"no options":         {QuestionID: "rain", Positive: []string{"wet"}, Negative: []string{"dry"}},
		"no positive":        {QuestionID: "rain", Options: []string{"yes"}, Negative: []string{"dry"}},
		"no negative":        {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}},
		"overlap":            {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry", "wet"}},
		"duplicate option":   {QuestionID: "rain", Options: []string{"yes", "yes"}, Positive: []string{"wet"}, Negative: []string{"dry"}},
		"duplicate positive": {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet", "wet"}, Negative: []string{"dry"}},
		"state in two lists": {QuestionID: "rain", Options: []string{"yes"}, Positive: []string{"wet"}, Negative: []string{"dry", "fog"}, Inconclusive: []string{"fog"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.Calibrate(context.Background(), docs, index, p, app.CalibrateOptions{}); err == nil || !strings.HasPrefix(err.Error(), "calibrate: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestAnEmptyRecordIsAnEmptyReport(t *testing.T) {
	docs, index := record(t)
	c, err := app.Calibrate(context.Background(), docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil || c.N != 0 || c.Brier != nil || len(c.Bins) != 10 {
		t.Errorf("c = %+v err = %v", c, err)
	}
}

func TestAJudgementThatNeverAskedTheQuestionIsNotInTheSample(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	q := []ports.Question{{ID: "wind", Kind: ports.KindNoul, Ask: "Will it be windy?"}}
	j := forecast("breezy", 0.5, day)
	j.Answers[0].ID = "wind"
	path, err := app.RecordJudgement(ctx, docs, index, "breezy", q, j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
		t.Fatal(err)
	}
	c, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{})
	if err != nil || c.N != 0 || c.Excluded != (app.ExclusionCounts{}) {
		t.Errorf("c = %+v err = %v", c, err)
	}
}

func TestCalibrationIsReportedPerEngineAndPooled(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i, engine := range []struct{ provider, model string }{{"local", "small"}, {"local", "small"}, {"remote", "large"}} {
		j := forecast(fmt.Sprintf("s-%d", i), 0.7, day.Add(time.Duration(i)*time.Minute))
		j.Provider, j.Model = engine.provider, engine.model
		path, err := app.RecordJudgement(ctx, docs, index, fmt.Sprintf("s-%d", i), rainQuestion(), j)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.AttachOutcome(ctx, docs, index, path, app.Outcome{State: "wet", When: day}); err != nil {
			t.Fatal(err)
		}
	}
	pooled, byEngine, err := app.CalibrateByEngine(ctx, docs, index, rainPrediction())
	if err != nil {
		t.Fatal(err)
	}
	if pooled.N != 3 || len(byEngine) != 2 {
		t.Fatalf("pooled N = %d engines = %d", pooled.N, len(byEngine))
	}
	if byEngine[0].Provider != "local" || byEngine[0].Model != "small" || byEngine[0].N != 2 || byEngine[1].N != 1 {
		t.Errorf("by engine = %+v", byEngine)
	}
	one, err := app.Calibrate(ctx, docs, index, rainPrediction(), app.CalibrateOptions{Provider: "remote"})
	if err != nil || one.N != 1 {
		t.Errorf("remote only: N = %d err = %v", one.N, err)
	}
}

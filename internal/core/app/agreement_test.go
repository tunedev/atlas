package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestTheAgreementRateCountsDecisionsThatMatchedTheVerdict(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range 40 {
		verdict, choice := "yes", "yes"
		if i < 12 {
			choice = "no"
		}
		d := app.Decision{SubjectID: fmt.Sprintf("s-%d", i), Choice: choice, VerdictAtDecision: verdict, When: day.Add(time.Duration(i) * time.Minute)}
		if _, err := app.RecordDecision(ctx, docs, index, d); err != nil {
			t.Fatal(err)
		}
	}
	// A decision with no judgement before it is not a comparison.
	if _, err := app.RecordDecision(ctx, docs, index, app.Decision{SubjectID: "manual", Choice: "yes", When: day}); err != nil {
		t.Fatal(err)
	}
	a, err := app.AgreementRate(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	if a.N != 40 || a.Agreed != 28 || a.Rate == nil || *a.Rate != 28.0/40 {
		t.Errorf("agreement = %+v", a)
	}
}

func TestBelowThirtyDecisionsThereIsNoAgreementRate(t *testing.T) {
	ctx := context.Background()
	docs, index := record(t)
	for i := range app.MinSample - 1 {
		d := app.Decision{SubjectID: fmt.Sprintf("s-%d", i), Choice: "yes", VerdictAtDecision: "yes", When: day.Add(time.Duration(i) * time.Minute)}
		if _, err := app.RecordDecision(ctx, docs, index, d); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.AgreementRate(ctx, index)
	if err != nil || a.N != app.MinSample-1 || a.Rate != nil {
		t.Errorf("agreement = %+v err = %v", a, err)
	}
}

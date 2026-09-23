package app_test

import (
	"math"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func recordedYesNo() ports.Completion {
	return ports.Completion{
		Text: `{"answer": "yes"}`,
		Tokens: []ports.Token{
			{Text: "{", LogProb: -11.5, Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.042}, {Text: "The", LogProb: -3.75}, {Text: "No", LogProb: -4.98}}},
			{Text: "answer", LogProb: -4.95, Alternatives: []ports.Alternative{
				{Text: "text", LogProb: -1.35}, {Text: "response", LogProb: -1.63}}},
			{Text: "yes", LogProb: -1.3244, Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.3975}, {Text: "yes", LogProb: -1.3244}, {Text: "no", LogProb: -4.3892}}},
		},
	}
}

var yesNo = map[string][]string{
	"yes": {"yes", "Yes", "YES", "true"},
	"no":  {"no", "No", "NO", "false"},
}

func TestMassSumsAcrossSurfaceForms(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	// "yes" (0.266) plus "Yes" (0.672) against "no" (0.012), normalised.
	if got["yes"] < 0.90 {
		t.Errorf("yes = %.4f, want at least 0.90; surface forms are not being summed", got["yes"])
	}
	if got["no"] > 0.05 {
		t.Errorf("no = %.4f, want below 0.05", got["no"])
	}
}

func TestMassIsNotTheChosenTokensProbability(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	chosen := math.Exp(-1.3244) // the "yes" token as emitted
	if math.Abs(got["yes"]-chosen) < 0.01 {
		t.Errorf("yes = %.4f, which is the chosen token's own probability; the split across surface forms was ignored", got["yes"])
	}
}

// The recorded fixture cannot separate "read the structural token" from "read
// the answer token": both favour yes, within a percentage point of each other.
// These two fixtures are built so each rule fails visibly when broken.

func TestAStructuralTokenIsSkippedEvenWhenItsAlternativesLookLikeClasses(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{
		// A brace the schema forced. Its alternatives are the prose the model
		// wanted, and they point the opposite way to the real answer.
		{Text: "{", LogProb: math.Log(0.001), Alternatives: []ports.Alternative{
			{Text: "No", LogProb: math.Log(0.97)},
			{Text: "Yes", LogProb: math.Log(0.02)}}},
		{Text: "yes", LogProb: math.Log(0.88), Alternatives: []ports.Alternative{
			{Text: "yes", LogProb: math.Log(0.88)},
			{Text: "no", LogProb: math.Log(0.04)}}},
	}}
	got, err := app.MassPerClass(c, yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	if got["yes"] < 0.9 {
		t.Errorf("yes = %.4f; the structural token was read instead of the answer token", got["yes"])
	}
}

func TestTheLastClassBearingTokenWins(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{
		{Text: "yes", LogProb: math.Log(0.5), Alternatives: []ports.Alternative{
			{Text: "yes", LogProb: math.Log(0.5)},
			{Text: "no", LogProb: math.Log(0.5)}}},
		{Text: "no", LogProb: math.Log(0.93), Alternatives: []ports.Alternative{
			{Text: "no", LogProb: math.Log(0.93)},
			{Text: "yes", LogProb: math.Log(0.05)}}},
	}}
	got, err := app.MassPerClass(c, yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	if got["no"] < 0.9 {
		t.Errorf("no = %.4f; an earlier class-bearing token was read instead of the last", got["no"])
	}
}

func TestMassSumsToOne(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	total := 0.0
	for _, v := range got {
		total += v
	}
	if math.Abs(total-1.0) > 1e-9 {
		t.Errorf("mass sums to %.6f, want 1", total)
	}
}

func TestACompletionWithNoTokensIsAnError(t *testing.T) {
	if _, err := app.MassPerClass(ports.Completion{Text: "yes"}, yesNo); err == nil {
		t.Fatal("a completion carrying no per-token data returned no error")
	}
}

func TestNoMatchingTokenIsAnError(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{{Text: "banana", Alternatives: []ports.Alternative{{Text: "apple", LogProb: -1}}}}}
	if _, err := app.MassPerClass(c, yesNo); err == nil {
		t.Fatal("a completion with no token matching any class returned no error")
	}
}

func TestTheChosenTokenIsNotCountedTwice(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{
		{Text: "yes", LogProb: math.Log(0.3), Alternatives: []ports.Alternative{
			{Text: "yes", LogProb: math.Log(0.3)},
			{Text: "no", LogProb: math.Log(0.6)}}},
	}}
	got, err := app.MassPerClass(c, yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	want := 1.0 / 3.0
	if math.Abs(got["yes"]-want) > 0.01 {
		t.Errorf("yes = %.4f, want %.4f; the chosen token's own text is already among its alternatives, so counting it again double-counts that surface form", got["yes"], want)
	}
}

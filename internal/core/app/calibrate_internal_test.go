package app

import (
	"math"
	"testing"
)

func repeat(n int, p float64, happened bool) []scoredPoint {
	out := make([]scoredPoint, n)
	for i := range out {
		out[i] = scoredPoint{predicted: p, happened: happened}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTheBrierScoreIsTheMeanSquaredErrorOfTheProbabilities(t *testing.T) {
	// 20 at 0.8 that happened, 10 at 0.8 that did not, 5 at 0.2 that did not:
	// (20*0.04 + 10*0.64 + 5*0.04) / 35 = 7.4/35.
	points := append(append(repeat(20, 0.8, true), repeat(10, 0.8, false)...), repeat(5, 0.2, false)...)
	c := score(points)
	if c.N != 35 {
		t.Fatalf("N = %d", c.N)
	}
	if c.Brier == nil || !near(*c.Brier, 7.4/35) {
		t.Errorf("Brier = %v, want %v", c.Brier, 7.4/35)
	}
}

func TestBelowThirtyPointsThereIsNoBrierScore(t *testing.T) {
	c := score(repeat(MinSample-1, 0.8, true))
	if c.N != MinSample-1 || c.Brier != nil {
		t.Errorf("N = %d Brier = %v; below %d the count is reported and the number is not", c.N, c.Brier, MinSample)
	}
	if score(repeat(MinSample, 0.8, true)).Brier == nil {
		t.Error("exactly the minimum sample has no Brier score")
	}
}

func TestBinsAreFixedDecilesAndReadOnlyWithTenPoints(t *testing.T) {
	// The 0.8 bin: 12 points, 7 happened -> 7/12. The 0.3 bin: 9 points -> too few.
	points := append(append(repeat(7, 0.83, true), repeat(5, 0.81, false)...), repeat(9, 0.3, true)...)
	c := score(points)
	if len(c.Bins) != 10 {
		t.Fatalf("bins = %d, want 10", len(c.Bins))
	}
	for i, b := range c.Bins {
		if !near(b.Low, float64(i)/10) || !near(b.High, float64(i+1)/10) {
			t.Errorf("bin %d = [%v,%v)", i, b.Low, b.High)
		}
	}
	eight := c.Bins[8]
	if eight.N != 12 || eight.ObservedRate == nil || !near(*eight.ObservedRate, 7.0/12) || !near(eight.MeanPredicted, (7*0.83+5*0.81)/12) {
		t.Errorf("0.8 bin = %+v", eight)
	}
	three := c.Bins[3]
	if three.N != 9 || three.ObservedRate != nil {
		t.Errorf("0.3 bin = %+v; nine points must not print a rate", three)
	}
}

func TestTheEdgesOfTheProbabilityRangeLandInTheEndBins(t *testing.T) {
	c := score([]scoredPoint{{0.0, false}, {1.0, true}, {0.1, false}, {0.9999, true}})
	if c.Bins[0].N != 1 || c.Bins[9].N != 2 || c.Bins[1].N != 1 {
		t.Errorf("bin counts = %d %d %d; 0.0 in the first, 1.0 and 0.9999 in the last, 0.1 in the second",
			c.Bins[0].N, c.Bins[9].N, c.Bins[1].N)
	}
}

func TestNoPointsIsAnEmptyReportNotAnError(t *testing.T) {
	c := score(nil)
	if c.N != 0 || c.Brier != nil || len(c.Bins) != 10 {
		t.Errorf("empty = %+v", c)
	}
}

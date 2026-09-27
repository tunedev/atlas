package app

// MinSample is the fewest scored points a headline Brier score is reported
// for, and MinBinSample the fewest one reliability row is: below them the
// standard error of an observed proportion is too wide for the number to
// mean anything, so only the count is reported.
const (
	MinSample    = 30
	MinBinSample = 10
)

// Calibration is how a set of predicted probabilities fared against what
// happened. Brier is nil below MinSample; each bin's ObservedRate is nil
// below MinBinSample.
type Calibration struct {
	N        int
	Excluded ExclusionCounts
	Brier    *float64
	Bins     []ReliabilityBin
}

// ExclusionCounts is how many judgements were left out of a sample, and why.
type ExclusionCounts struct {
	Pending        int // no outcome attached yet
	ZeroCoverage   int // the scored answer's alternatives named none of its options
	Unclassified   int // an outcome the prediction names neither positive nor negative
	OptionMismatch int // a predicted option the judgement's question does not declare
}

// ReliabilityBin is one fixed-width decile of predicted probability: how
// many points fell in it, their mean prediction, and how often the
// predicted event happened.
type ReliabilityBin struct {
	Low, High     float64
	N             int
	MeanPredicted float64
	ObservedRate  *float64
}

// scoredPoint is one resolved prediction: the probability given, and
// whether the event happened.
type scoredPoint struct {
	predicted float64
	happened  bool
}

// score computes the Brier score and the ten reliability bins of points.
// The last bin is closed, so a probability of exactly 1 falls in it.
func score(points []scoredPoint) Calibration {
	c := Calibration{N: len(points), Bins: make([]ReliabilityBin, 10)}
	var sumSq float64
	hits := make([]int, 10)
	sums := make([]float64, 10)
	for _, p := range points {
		sumSq += (p.predicted - outcomeValue(p.happened)) * (p.predicted - outcomeValue(p.happened))
		i := min(max(int(p.predicted*10), 0), 9)
		c.Bins[i].N++
		sums[i] += p.predicted
		if p.happened {
			hits[i]++
		}
	}
	if c.N >= MinSample {
		brier := sumSq / float64(c.N)
		c.Brier = &brier
	}
	for i := range c.Bins {
		b := &c.Bins[i]
		b.Low, b.High = float64(i)/10, float64(i+1)/10
		if b.N > 0 {
			b.MeanPredicted = sums[i] / float64(b.N)
		}
		if b.N >= MinBinSample {
			rate := float64(hits[i]) / float64(b.N)
			b.ObservedRate = &rate
		}
	}
	return c
}

func outcomeValue(happened bool) float64 {
	if happened {
		return 1
	}
	return 0
}

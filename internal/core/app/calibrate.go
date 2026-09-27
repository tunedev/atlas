package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

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
	Inconclusive   int // resolved, State is in Prediction.Inconclusive by pack declaration
	Unclassified   int // resolved, but State is in none of Positive, Negative, or Inconclusive
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

// Prediction names what a calibration run scores. QuestionID is the
// recorded answer that predicts a real-world result; the summed mass of its
// Options is the predicted probability of a Positive outcome. Positive,
// Negative and Inconclusive are outcome states a pack declares, sorting
// every resolved outcome into one of three dispositions: it came true, it
// came false, or it is not evidence either way. An outcome in none of the
// three is Unclassified — left out and counted, never scored as a miss.
type Prediction struct {
	QuestionID   string
	Options      []string
	Positive     []string
	Negative     []string
	Inconclusive []string
}

// CalibrateOptions narrows a run to one provider or one model; empty
// pools them.
type CalibrateOptions struct {
	Provider string
	Model    string
}

// EngineCalibration is a Calibration for one provider and model.
type EngineCalibration struct {
	Provider, Model string
	Calibration
}

func (p Prediction) validate() error {
	switch {
	case p.QuestionID == "":
		return errors.New("calibrate: no question id")
	case len(p.Options) == 0:
		return errors.New("calibrate: no predicted options")
	case len(p.Positive) == 0 || len(p.Negative) == 0:
		return errors.New("calibrate: positive and negative outcome states are both needed")
	}
	if err := disjoint("positive", p.Positive, "negative", p.Negative); err != nil {
		return err
	}
	if err := disjoint("positive", p.Positive, "inconclusive", p.Inconclusive); err != nil {
		return err
	}
	if err := disjoint("negative", p.Negative, "inconclusive", p.Inconclusive); err != nil {
		return err
	}
	if err := noDuplicate("option", p.Options); err != nil {
		return err
	}
	if err := noDuplicate("positive", p.Positive); err != nil {
		return err
	}
	if err := noDuplicate("negative", p.Negative); err != nil {
		return err
	}
	if err := noDuplicate("inconclusive", p.Inconclusive); err != nil {
		return err
	}
	return nil
}

// disjoint reports a state common to two Prediction lists: a pack that
// lists the same state under two of Positive, Negative and Inconclusive has
// not made up its mind about it.
func disjoint(aLabel string, a []string, bLabel string, b []string) error {
	for _, s := range a {
		if slices.Contains(b, s) {
			return fmt.Errorf("calibrate: %q is both %s and %s", s, aLabel, bLabel)
		}
	}
	return nil
}

// noDuplicate reports a repeated entry in a Prediction list as an error
// naming the list and the entry, since a repeat there is the same typo class
// as a naming mismatch elsewhere in the prediction.
func noDuplicate(label string, values []string) error {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return fmt.Errorf("calibrate: %s %q is listed twice", label, v)
		}
		seen[v] = true
	}
	return nil
}

// Calibrate scores every judgement that asked p.QuestionID and has an
// outcome p classifies, against the mass it gave p.Options.
func Calibrate(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction, opts CalibrateOptions) (Calibration, error) {
	if err := p.validate(); err != nil {
		return Calibration{}, err
	}
	match := map[string]string{}
	if opts.Provider != "" {
		match["provider"] = opts.Provider
	}
	if opts.Model != "" {
		match["model"] = opts.Model
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement", Match: match})
	if err != nil {
		return Calibration{}, fmt.Errorf("calibrate: find: %w", err)
	}
	return calibrateRows(ctx, docs, rows, p)
}

// CalibrateByEngine scores p over every engine pooled, and over each
// provider and model on its own, ordered by provider then model.
func CalibrateByEngine(ctx context.Context, docs ports.Docs, index ports.Index, p Prediction) (Calibration, []EngineCalibration, error) {
	if err := p.validate(); err != nil {
		return Calibration{}, nil, err
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "judgement"})
	if err != nil {
		return Calibration{}, nil, fmt.Errorf("calibrate: find: %w", err)
	}
	pooled, err := calibrateRows(ctx, docs, rows, p)
	if err != nil {
		return Calibration{}, nil, err
	}
	groups := map[[2]string][]ports.Record{}
	for _, r := range rows {
		key := [2]string{r.Fields["provider"], r.Fields["model"]}
		groups[key] = append(groups[key], r)
	}
	keys := make([][2]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	var byEngine []EngineCalibration
	for _, k := range keys {
		c, err := calibrateRows(ctx, docs, groups[k], p)
		if err != nil {
			return Calibration{}, nil, err
		}
		byEngine = append(byEngine, EngineCalibration{Provider: k[0], Model: k[1], Calibration: c})
	}
	return pooled, byEngine, nil
}

// scoredDoc is the part of a judgement document a calibration reads.
type scoredDoc struct {
	Questions []judgementQuestion `json:"questions"`
	Answers   []judgementAnswer   `json:"answers"`
	Outcome   *outcomeDoc         `json:"outcome"`
}

func calibrateRows(ctx context.Context, docs ports.Docs, rows []ports.Record, p Prediction) (Calibration, error) {
	var points []scoredPoint
	var excluded ExclusionCounts
	for _, r := range rows {
		if !slices.Contains(strings.Split(r.Fields["questions"], ","), p.QuestionID) {
			continue
		}
		body, err := docs.Get(ctx, r.Path)
		if err != nil {
			return Calibration{}, fmt.Errorf("calibrate: read %s: %w", r.Path, err)
		}
		var doc scoredDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return Calibration{}, fmt.Errorf("calibrate: decode %s: %w", r.Path, err)
		}
		pt, reason, err := p.point(r.Path, doc)
		if err != nil {
			return Calibration{}, err
		}
		switch reason {
		case "":
			points = append(points, pt)
		case "pending":
			excluded.Pending++
		case "option_mismatch":
			excluded.OptionMismatch++
		case "zero_coverage":
			excluded.ZeroCoverage++
		case "inconclusive":
			excluded.Inconclusive++
		case "unclassified":
			excluded.Unclassified++
		}
	}
	c := score(points)
	c.Excluded = excluded
	return c, nil
}

// point reads one judgement document as a scored point, or names why it is
// left out. A document that lists the question but has no answer for it is
// an error: the record contradicts itself.
func (p Prediction) point(path string, doc scoredDoc) (scoredPoint, string, error) {
	if doc.Outcome == nil {
		return scoredPoint{}, "pending", nil
	}
	i := slices.IndexFunc(doc.Answers, func(a judgementAnswer) bool { return a.ID == p.QuestionID })
	if i < 0 {
		return scoredPoint{}, "", fmt.Errorf("calibrate: %s has no answer %q", path, p.QuestionID)
	}
	answer := doc.Answers[i]
	if !p.declared(doc, answer) {
		return scoredPoint{}, "option_mismatch", nil
	}
	if answer.Coverage.Represented == 0 {
		return scoredPoint{}, "zero_coverage", nil
	}
	if slices.Contains(p.Inconclusive, doc.Outcome.State) {
		return scoredPoint{}, "inconclusive", nil
	}
	var happened bool
	switch {
	case slices.Contains(p.Positive, doc.Outcome.State):
		happened = true
	case slices.Contains(p.Negative, doc.Outcome.State):
		happened = false
	default:
		return scoredPoint{}, "unclassified", nil
	}
	var mass float64
	for _, o := range p.Options {
		mass += answer.Distribution[o]
	}
	return scoredPoint{predicted: mass, happened: happened}, "", nil
}

// declared reports whether the question answer came from declares every
// predicted option. A noul with no options of its own declares yes and no.
func (p Prediction) declared(doc scoredDoc, answer judgementAnswer) bool {
	var options []string
	for _, q := range doc.Questions {
		if q.ID == answer.ID {
			options = q.Options
			if len(options) == 0 && q.Kind == string(ports.KindNoul) {
				options = ports.NoulOptions()
			}
		}
	}
	for _, o := range p.Options {
		if !slices.Contains(options, o) {
			return false
		}
	}
	return true
}

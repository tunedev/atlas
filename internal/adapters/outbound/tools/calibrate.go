package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Calibrate reports how recorded predictions fared against attached
// outcomes, pooled and per engine, in words a person can read aloud.
type Calibrate struct {
	docs  ports.Docs
	index ports.Index
}

func NewCalibrate(docs ports.Docs, index ports.Index) *Calibrate {
	return &Calibrate{docs: docs, index: index}
}

func (t *Calibrate) Name() string { return "judge.calibrate" }

func (t *Calibrate) Invoke(ctx context.Context, with map[string]string) (any, error) {
	p := app.Prediction{
		QuestionID: strings.TrimSpace(with["question"]),
		Options:    list(with["options"]),
		Positive:   list(with["positive"]),
		Negative:   list(with["negative"]),
	}
	pooled, byEngine, err := app.CalibrateByEngine(ctx, t.docs, t.index, p)
	if err != nil {
		return nil, fmt.Errorf("judge.calibrate: %w", err)
	}
	engines := make([]any, len(byEngine))
	for i, e := range byEngine {
		r := report(e.Calibration)
		r["provider"], r["model"] = e.Provider, e.Model
		engines[i] = r
	}
	return map[string]any{
		"prediction": map[string]any{"question": p.QuestionID, "options": p.Options, "positive": p.Positive, "negative": p.Negative},
		"pooled":     report(pooled),
		"by_engine":  engines,
	}, nil
}

// report renders a Calibration, saying "too few" wherever the core
// withheld a number.
func report(c app.Calibration) map[string]any {
	headline := fmt.Sprintf("n=%d, too few for a Brier score (needs %d)", c.N, app.MinSample)
	var brier any
	if c.Brier != nil {
		brier = *c.Brier
		headline = fmt.Sprintf("Brier %.3f over n=%d", *c.Brier, c.N)
	}
	bins := make([]any, len(c.Bins))
	for i, b := range c.Bins {
		reads := fmt.Sprintf("n=%d, too few (needs %d)", b.N, app.MinBinSample)
		var rate any
		if b.ObservedRate != nil {
			rate = *b.ObservedRate
			reads = fmt.Sprintf("your %.0f%% judgements came true %.0f%% of the time (n=%d)", b.Low*100, *b.ObservedRate*100, b.N)
		}
		bins[i] = map[string]any{"low": b.Low, "high": b.High, "n": b.N, "mean_predicted": b.MeanPredicted, "observed_rate": rate, "reads": reads}
	}
	return map[string]any{
		"n": c.N,
		"excluded": map[string]any{
			"pending":         c.Excluded.Pending,
			"zero_coverage":   c.Excluded.ZeroCoverage,
			"unclassified":    c.Excluded.Unclassified,
			"option_mismatch": c.Excluded.OptionMismatch,
		},
		"brier":    brier,
		"headline": headline,
		"bins":     bins,
	}
}

// list splits a comma list, dropping blanks.
func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

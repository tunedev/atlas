package search

import (
	"cmp"
	"slices"
)

type Outcome string

const (
	Met     Outcome = "met"
	Unmet   Outcome = "unmet"
	Unknown Outcome = "unknown"
	Partial Outcome = "partial"
	NA      Outcome = "n/a"
)

type Result struct {
	Version      int    `json:"version"`
	CriteriaHash string `json:"criteria_hash"`
	Checked      int    `json:"checked"`
	Hidden       int    `json:"hidden"`
	Rows         []Row  `json:"rows"`
}

type Row struct {
	SubjectID   string            `json:"subject_id"`
	Score       float64           `json:"score"`
	Known       float64           `json:"known"`
	MustUnknown []string          `json:"must_unknown,omitempty"`
	Results     []CriterionResult `json:"results"`
	Display     map[string]any    `json:"display"`
}

type CriterionResult struct {
	ID       string  `json:"id"`
	Outcome  Outcome `json:"outcome"`
	Evidence string  `json:"evidence,omitempty"`
}

// Match checks every enrichment against c and ranks what is not hidden.
// c must come from ParseCriteria.
func Match(c Criteria, es []Enrichment) Result {
	res := Result{Version: 1, CriteriaHash: c.Hash, Checked: len(es), Rows: []Row{}}
	for i := range es {
		row, hidden := matchOne(c, &es[i])
		if hidden {
			res.Hidden++
			continue
		}
		res.Rows = append(res.Rows, row)
	}
	slices.SortFunc(res.Rows, compareRows)
	return res
}

func matchOne(c Criteria, e *Enrichment) (Row, bool) {
	row := Row{SubjectID: e.SubjectID, Results: []CriterionResult{}, Display: map[string]any{}}
	var total, earned, known float64
	for _, cr := range c.Criteria {
		r := judge(cr, e)
		row.Results = append(row.Results, r)
		if cr.Must {
			switch r.Outcome {
			case Unmet:
				return Row{}, true
			case Unknown:
				row.MustUnknown = append(row.MustUnknown, cr.ID)
			}
			continue
		}
		w := float64(cr.Weight)
		switch r.Outcome {
		case NA:
			continue
		case Met:
			earned += w
			known += w
		case Partial:
			earned += w / 2
			known += w
		case Unmet:
			known += w
		}
		total += w
	}
	if total > 0 {
		row.Score = earned / total
		row.Known = known / total
	}
	for _, path := range c.Display {
		row.Display[path] = e.Value(path)
	}
	return row, false
}

// judge applies the outcome rules: when, then check, then partial.
func judge(cr Criterion, e *Enrichment) CriterionResult {
	r := CriterionResult{ID: cr.ID}
	if cr.When != nil {
		if o, _ := eval(*cr.When, e); o == Unmet {
			r.Outcome = NA
			return r
		}
	}
	check, checkEvidence := eval(cr.Check, e)
	if check == Met {
		r.Outcome, r.Evidence = Met, checkEvidence
		return r
	}
	partial := Unmet
	if cr.Partial != nil {
		var partialEvidence string
		partial, partialEvidence = eval(*cr.Partial, e)
		if partial == Met {
			r.Outcome, r.Evidence = Partial, partialEvidence
			return r
		}
	}
	if check == Unknown || partial == Unknown {
		r.Outcome = Unknown
		return r
	}
	r.Outcome, r.Evidence = Unmet, checkEvidence
	return r
}

// eval returns a condition's outcome and the evidence of the comparison
// that decided it.
func eval(c Condition, e *Enrichment) (Outcome, string) {
	switch {
	case len(c.Any) > 0:
		return combine(c.Any, e, Met, Unmet)
	case len(c.All) > 0:
		return combine(c.All, e, Unmet, Met)
	}
	met, ok := compare(c.Op, e.Value(c.Field), c.Value)
	switch {
	case !ok:
		return Unknown, ""
	case met:
		return Met, e.Evidence[c.Field]
	}
	return Unmet, e.Evidence[c.Field]
}

// combine evaluates children: the first child with outcome decisive decides;
// otherwise any unknown child makes it unknown; otherwise it is fallback,
// evidenced by the first child.
func combine(children []Condition, e *Enrichment, decisive, fallback Outcome) (Outcome, string) {
	unknown := false
	var firstEvidence string
	for i, child := range children {
		o, evidence := eval(child, e)
		if o == decisive {
			return o, evidence
		}
		if o == Unknown {
			unknown = true
		}
		if i == 0 {
			firstEvidence = evidence
		}
	}
	if unknown {
		return Unknown, ""
	}
	return fallback, firstEvidence
}

// compare applies op to a stored value and a validated criteria value. ok is
// false when the stored value is null or not of the type op compares.
func compare(op string, got, want any) (met, ok bool) {
	switch op {
	case "==":
		switch w := want.(type) {
		case string:
			g, ok := got.(string)
			return g == w, ok
		case bool:
			g, ok := got.(bool)
			return g == w, ok
		case float64:
			g, ok := got.(float64)
			return g == w, ok
		}
	case "in":
		g, ok := got.(string)
		return slices.Contains(want.([]any), any(g)), ok
	case "contains_any":
		g, ok := got.([]any)
		return slices.ContainsFunc(want.([]any), func(w any) bool { return slices.Contains(g, w) }), ok
	case ">=":
		g, ok := got.(float64)
		return g >= want.(float64), ok
	case "<=":
		g, ok := got.(float64)
		return g <= want.(float64), ok
	case "overlaps":
		lo, hi, ok := pair(got)
		w := want.([]any)
		return lo <= w[1].(float64) && w[0].(float64) <= hi, ok
	}
	return false, false
}

// pair reads a stored [min, max] range.
func pair(v any) (lo, hi float64, ok bool) {
	p, ok := v.([]any)
	if !ok || len(p) != 2 {
		return 0, 0, false
	}
	lo, loOK := p[0].(float64)
	hi, hiOK := p[1].(float64)
	return lo, hi, loOK && hiOK
}

// compareRows orders full passes before rows with unknown must-haves, then
// by score, then known, then subject id.
func compareRows(a, b Row) int {
	return cmp.Or(
		cmp.Compare(boolInt(len(a.MustUnknown) > 0), boolInt(len(b.MustUnknown) > 0)),
		cmp.Compare(b.Score, a.Score),
		cmp.Compare(b.Known, a.Known),
		cmp.Compare(a.SubjectID, b.SubjectID),
	)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

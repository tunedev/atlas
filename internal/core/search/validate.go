package search

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

var criterionID = regexp.MustCompile(`^[a-z0-9-]+$`)

// validate reports every problem in a criteria set, joined into one error.
func validate(c Criteria, f Fields) error {
	var errs []error
	if c.Version != 1 {
		errs = append(errs, fmt.Errorf("version %d is not supported; want 1", c.Version))
	}
	seen := map[string]bool{}
	for _, cr := range c.Criteria {
		errs = append(errs, validateCriterion(f, cr, seen)...)
	}
	for _, path := range c.Display {
		if !f.displayable(path) {
			errs = append(errs, fmt.Errorf("display %q is not a declared field", path))
		}
	}
	return errors.Join(errs...)
}

func validateCriterion(f Fields, cr Criterion, seen map[string]bool) []error {
	var errs []error
	at := fmt.Sprintf("criterion %q", cr.ID)
	if !criterionID.MatchString(cr.ID) {
		errs = append(errs, fmt.Errorf("%s: id must match %s", at, criterionID))
	}
	if seen[cr.ID] {
		errs = append(errs, fmt.Errorf("%s: id repeats", at))
	}
	seen[cr.ID] = true
	if strings.TrimSpace(cr.Statement) == "" {
		errs = append(errs, fmt.Errorf("%s: statement is empty", at))
	}
	if cr.Must == (cr.Weight != nil) {
		errs = append(errs, fmt.Errorf("%s: needs exactly one of must and weight", at))
	}
	if cr.Weight != nil && *cr.Weight < 1 {
		errs = append(errs, fmt.Errorf("%s: weight must be positive", at))
	}
	if cr.Must && cr.Partial != nil {
		errs = append(errs, fmt.Errorf("%s: a must-have cannot have partial", at))
	}
	errs = append(errs, validateCondition(f, at+" check", cr.Check)...)
	if cr.Partial != nil {
		errs = append(errs, validateCondition(f, at+" partial", *cr.Partial)...)
	}
	if cr.When != nil {
		errs = append(errs, validateCondition(f, at+" when", *cr.When)...)
	}
	return errs
}

func validateCondition(f Fields, at string, c Condition) []error {
	comparison := c.Field != "" || c.Op != "" || c.Value != nil
	combinators := 0
	for _, children := range [][]Condition{c.Any, c.All} {
		if len(children) > 0 {
			combinators++
		}
	}
	switch {
	case comparison && combinators > 0, combinators > 1:
		return []error{fmt.Errorf("%s: a condition is one comparison, one any, or one all", at)}
	case combinators == 1:
		var errs []error
		for i, child := range slices.Concat(c.Any, c.All) {
			errs = append(errs, validateCondition(f, fmt.Sprintf("%s[%d]", at, i), child)...)
		}
		return errs
	case !comparison:
		return []error{fmt.Errorf("%s: empty condition", at)}
	}
	return validateComparison(f, at, c)
}

func validateComparison(f Fields, at string, c Condition) []error {
	fd, ok := f.ByPath[c.Field]
	if !ok || len(ops[fd.Kind]) == 0 {
		return []error{fmt.Errorf("%s: field %q is not a checkable declared field", at, c.Field)}
	}
	if !slices.Contains(ops[fd.Kind], c.Op) {
		return []error{fmt.Errorf("%s: op %q does not apply to %s; want one of %v", at, c.Op, c.Field, ops[fd.Kind])}
	}
	if err := f.validateValue(fd, c.Op, c.Value); err != nil {
		return []error{fmt.Errorf("%s: %s: %w", at, c.Field, err)}
	}
	return nil
}

func (f Fields) validateValue(fd Field, op string, v any) error {
	switch fd.Kind {
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("value %v is not a boolean", v)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("value %v is not a number", v)
		}
	case "scalar":
		if op == "==" {
			return f.validateWord(fd, v)
		}
		return f.validateWords(fd, v)
	case "list":
		return f.validateWords(fd, v)
	case "range":
		return validateRange(fd, v)
	}
	return nil
}

func (f Fields) validateWords(fd Field, v any) error {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return fmt.Errorf("value %v is not a non-empty list", v)
	}
	var errs []error
	for _, item := range items {
		errs = append(errs, f.validateWord(fd, item))
	}
	return errors.Join(errs...)
}

func (f Fields) validateWord(fd Field, v any) error {
	s, ok := v.(string)
	if !ok || !f.allows(fd, s) {
		return fmt.Errorf("value %v is outside the vocabulary", v)
	}
	return nil
}

func validateRange(fd Field, v any) error {
	items, ok := v.([]any)
	if !ok || len(items) != 2 {
		return fmt.Errorf("value %v is not a [min, max] pair", v)
	}
	lo, loOK := items[0].(float64)
	hi, hiOK := items[1].(float64)
	whole := loOK && hiOK && lo == math.Trunc(lo) && hi == math.Trunc(hi)
	if !whole || lo < float64(*fd.Min) || hi > float64(*fd.Max) || lo > hi {
		return fmt.Errorf("value %v is not an integer range within %d to %d", v, *fd.Min, *fd.Max)
	}
	return nil
}

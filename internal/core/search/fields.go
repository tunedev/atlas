package search

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Fields is a pack's declaration of the fields its enricher fills.
type Fields struct {
	Version int               `json:"version"`
	ByPath  map[string]Field  `json:"fields"`
	Aliases map[string]string `json:"aliases"`
}

// Field declares one dotted path: its kind and the strings it allows.
type Field struct {
	Kind      string   `json:"kind"`
	Enum      []string `json:"enum,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Canonical bool     `json:"canonical,omitempty"`
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`

	re *regexp.Regexp
}

// ops lists the operators each kind accepts. A kind with none is display only.
var ops = map[string][]string{
	"scalar": {"==", "in"},
	"bool":   {"=="},
	"number": {"==", ">=", "<="},
	"list":   {"contains_any"},
	"range":  {"overlaps"},
	"text":   nil,
	"texts":  nil,
}

var fieldPath = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)

// ParseFields decodes a field declaration and reports every problem in it.
func ParseFields(data []byte) (Fields, error) {
	var f Fields
	if err := decodeStrict(data, &f); err != nil {
		return Fields{}, fmt.Errorf("fields: %w", err)
	}
	var errs []error
	if f.Version != 1 {
		errs = append(errs, fmt.Errorf("fields: version %d is not supported; want 1", f.Version))
	}
	paths := slices.Sorted(maps.Keys(f.ByPath))
	for i, path := range paths {
		fd, err := parseField(path, f.ByPath[path])
		errs = append(errs, err)
		f.ByPath[path] = fd
		if i+1 < len(paths) && strings.HasPrefix(paths[i+1], path+".") {
			errs = append(errs, fmt.Errorf("field %q: prefixes %q", path, paths[i+1]))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Fields{}, err
	}
	return f, nil
}

func parseField(path string, fd Field) (Field, error) {
	var errs []error
	at := fmt.Sprintf("field %q", path)
	if !fieldPath.MatchString(path) {
		errs = append(errs, fmt.Errorf("%s: path must match %s", at, fieldPath))
	}
	if _, ok := ops[fd.Kind]; !ok {
		errs = append(errs, fmt.Errorf("%s: kind %q is not one of scalar, bool, number, list, range, text, texts", at, fd.Kind))
	}
	if len(fd.Enum) > 0 && fd.Pattern != "" {
		errs = append(errs, fmt.Errorf("%s: has both enum and pattern", at))
	}
	if fd.Pattern != "" {
		re, err := regexp.Compile(fd.Pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: pattern: %w", at, err))
		}
		fd.re = re
	}
	if fd.Kind == "range" && (fd.Min == nil || fd.Max == nil) {
		errs = append(errs, fmt.Errorf("%s: a range needs min and max", at))
	}
	return fd, errors.Join(errs...)
}

// Normalize returns the canonical form of a string: trimmed, lowercased and
// mapped through the declaration's aliases.
func (f Fields) Normalize(s string) string {
	n := strings.ToLower(strings.TrimSpace(s))
	if canonical, ok := f.Aliases[n]; ok {
		return canonical
	}
	return n
}

// allows reports whether s is in fd's vocabulary.
func (f Fields) allows(fd Field, s string) bool {
	switch {
	case len(fd.Enum) > 0:
		return slices.Contains(fd.Enum, s)
	case fd.re != nil:
		return fd.re.MatchString(s)
	case fd.Canonical:
		return s != "" && f.Normalize(s) == s
	}
	return true
}

// displayable reports whether path is a declared field or a prefix of one.
func (f Fields) displayable(path string) bool {
	if _, ok := f.ByPath[path]; ok {
		return true
	}
	for p := range f.ByPath {
		if strings.HasPrefix(p, path+".") {
			return true
		}
	}
	return false
}

// decodeStrict decodes data into v and rejects keys v does not declare.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

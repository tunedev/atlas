package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// OptionsFor returns a question's effective options: its own, or the noul
// defaults when it is a noul with none given.
func OptionsFor(q ports.Question) []string {
	if len(q.Options) > 0 {
		return q.Options
	}
	if q.Kind == ports.KindNoul {
		return ports.NoulOptions()
	}
	return nil
}

// AnswerSchema builds standard JSON Schema bytes that constrain a model's
// reply to exactly the questions asked: one required string property per
// question, each restricted to that question's options by an enum, with no
// other property allowed. This is what makes a choice unable to answer
// outside its options.
//
// It is an error for qs to be empty, for two questions to share an id, for a
// question's id to be empty, for a kind to be neither noul, choice nor
// score, or for a choice or score to resolve to fewer than two options.
func AnswerSchema(qs []ports.Question) ([]byte, error) {
	if len(qs) == 0 {
		return nil, fmt.Errorf("judge: no questions to build a schema from")
	}

	properties := make(map[string]any, len(qs))
	required := make([]string, 0, len(qs))
	seen := make(map[string]bool, len(qs))

	for _, q := range qs {
		if err := validateQuestion(q, seen); err != nil {
			return nil, err
		}
		seen[q.ID] = true

		prop, err := propertyFor(q)
		if err != nil {
			return nil, err
		}
		properties[q.ID] = prop
		required = append(required, q.ID)
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
	return json.Marshal(schema)
}

// validateQuestion reports an error for an empty id or one already seen.
func validateQuestion(q ports.Question, seen map[string]bool) error {
	if q.ID == "" {
		return fmt.Errorf("judge: a question has no id")
	}
	if seen[q.ID] {
		return fmt.Errorf("judge: question id %q is used twice", q.ID)
	}
	return nil
}

// propertyFor builds the schema property for one question: a string
// constrained to its effective options by an enum.
func propertyFor(q ports.Question) (map[string]any, error) {
	switch q.Kind {
	case ports.KindNoul, ports.KindChoice, ports.KindScore:
	default:
		return nil, fmt.Errorf("judge: question %q has unknown kind %q", q.ID, q.Kind)
	}

	options := OptionsFor(q)
	if len(options) < 2 {
		return nil, fmt.Errorf("judge: question %q has fewer than two options", q.ID)
	}
	if err := validateOptionSet(q, options); err != nil {
		return nil, err
	}

	enum := make([]any, len(options))
	for i, o := range options {
		enum[i] = o
	}
	return map[string]any{"type": "string", "enum": enum}, nil
}

// optionString is one string that identifies option in q: the option's own
// name, or one of its surface forms.
type optionString struct {
	option, text string
}

// validateOptionSet rejects a question whose effective options and forms
// (classesFor(q, options): q.Forms, plus the noul defaults where they
// apply) cannot be told apart by a prefix-matching reader: two different
// options sharing an identical string, or one option's string being a
// proper prefix of another option's string. Both would let an exact or
// prefix match at read time silently resolve to the wrong option.
func validateOptionSet(q ports.Question, options []string) error {
	strs := optionStrings(classesFor(q, options))

	if err := rejectSharedForm(q, strs); err != nil {
		return err
	}
	return rejectPrefixCollision(q, strs)
}

// optionStrings flattens classes into one optionString per (option, form)
// pair.
func optionStrings(classes map[string][]string) []optionString {
	var out []optionString
	for option, forms := range classes {
		for _, form := range forms {
			out = append(out, optionString{option: option, text: form})
		}
	}
	return out
}

// rejectSharedForm errors when two different options in strs carry the
// identical text, naming the question and the shared form.
func rejectSharedForm(q ports.Question, strs []optionString) error {
	seen := make(map[string]string, len(strs))
	for _, s := range strs {
		owner, ok := seen[s.text]
		if ok && owner != s.option {
			return fmt.Errorf("judge: question %q: options %q and %q share the form %q", q.ID, owner, s.option, s.text)
		}
		seen[s.text] = s.option
	}
	return nil
}

// rejectPrefixCollision errors when one option's string in strs is a
// proper, non-empty prefix of a different option's string, naming the
// question and both colliding strings.
func rejectPrefixCollision(q ports.Question, strs []optionString) error {
	for _, a := range strs {
		for _, b := range strs {
			if a.option == b.option || a.text == b.text {
				continue
			}
			if strings.HasPrefix(b.text, a.text) {
				return fmt.Errorf("judge: question %q: %q is a prefix of %q", q.ID, a.text, b.text)
			}
		}
	}
	return nil
}

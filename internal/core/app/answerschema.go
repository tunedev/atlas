package app

import (
	"encoding/json"
	"fmt"

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

	enum := make([]any, len(options))
	for i, o := range options {
		enum[i] = o
	}
	return map[string]any{"type": "string", "enum": enum}, nil
}

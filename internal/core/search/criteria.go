package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Criteria is one search: must-haves that hide a subject when unmet, and
// weighted preferences that rank what is left.
type Criteria struct {
	Version  int         `json:"version"`
	Query    string      `json:"query"`
	Criteria []Criterion `json:"criteria"`
	Display  []string    `json:"display"`

	// Hash is the sha256 of the document ParseCriteria read.
	Hash string `json:"-"`
}

type Criterion struct {
	ID        string     `json:"id"`
	Statement string     `json:"statement"`
	Must      bool       `json:"must,omitempty"`
	Weight    int        `json:"weight,omitempty"`
	Check     Condition  `json:"check"`
	Partial   *Condition `json:"partial,omitempty"`
	When      *Condition `json:"when,omitempty"`
}

// Condition is a comparison (Field, Op, Value) or a combinator (Any or All).
type Condition struct {
	Field string      `json:"field,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value any         `json:"value,omitempty"`
	Any   []Condition `json:"any,omitempty"`
	All   []Condition `json:"all,omitempty"`
}

// ParseCriteria decodes a stored criteria document and validates it against
// a field declaration.
func ParseCriteria(data []byte, f Fields) (Criteria, error) {
	var c Criteria
	if err := decodeStrict(data, &c); err != nil {
		return Criteria{}, fmt.Errorf("criteria: %w", err)
	}
	if err := validate(c, f); err != nil {
		return Criteria{}, err
	}
	sum := sha256.Sum256(data)
	c.Hash = hex.EncodeToString(sum[:])
	return c, nil
}

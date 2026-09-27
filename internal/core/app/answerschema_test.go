package app_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func questions() []ports.Question {
	return []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "genre", Kind: ports.KindChoice, Ask: "Which genre?", Options: []string{"fiction", "history", "poetry"}},
		{ID: "length", Kind: ports.KindScore, Ask: "How long?", Options: []string{"short", "medium", "long"}},
	}
}

func decodeSchema(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("schema is not valid json: %v", err)
	}
	return m
}

func TestTheSchemaConstrainsEveryQuestionToItsOptions(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := decodeSchema(t, b)
	props, ok := s["properties"].(map[string]any)
	if !ok || len(props) != 3 {
		t.Fatalf("properties = %+v, want one per question", s["properties"])
	}
	genre, ok := props["genre"].(map[string]any)
	if !ok {
		t.Fatalf("no property for the choice question: %+v", props)
	}
	enum, ok := genre["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Fatalf("choice has no enum of its options: %+v", genre)
	}
}

func TestANoulIsConstrainedToYesAndNo(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	props := decodeSchema(t, b)["properties"].(map[string]any)
	enum := props["readable"].(map[string]any)["enum"].([]any)
	if len(enum) != 2 || enum[0] != "yes" || enum[1] != "no" {
		t.Errorf("noul enum = %v, want yes and no", enum)
	}
}

func TestEveryQuestionIsRequiredAndNothingElseIsAllowed(t *testing.T) {
	b, err := app.AnswerSchema(questions())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := decodeSchema(t, b)
	req, ok := s["required"].([]any)
	if !ok || len(req) != 3 {
		t.Errorf("required = %v, want every question id", s["required"])
	}
	if s["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false; the model may otherwise answer questions nobody asked", s["additionalProperties"])
	}
}

func TestAChoiceWithoutOptionsIsAnError(t *testing.T) {
	_, err := app.AnswerSchema([]ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Which genre?"}})
	if err == nil {
		t.Fatal("a choice with no options produced a schema that constrains nothing")
	}
}

func TestADuplicateQuestionIDIsAnError(t *testing.T) {
	qs := []ports.Question{
		{ID: "readable", Kind: ports.KindNoul, Ask: "Is it readable?"},
		{ID: "readable", Kind: ports.KindNoul, Ask: "Really?"},
	}
	if _, err := app.AnswerSchema(qs); err == nil {
		t.Fatal("two questions share an id and no error was returned; one answer would silently overwrite the other")
	}
}

func TestAnUnknownKindIsAnError(t *testing.T) {
	if _, err := app.AnswerSchema([]ports.Question{{ID: "x", Kind: "vibes", Ask: "?"}}); err == nil {
		t.Fatal("an unknown kind produced a schema")
	}
}

// TestAPrefixRelatedOptionSetIsRejected covers F1: "data" is a proper prefix
// of "database", so an emitted token of exactly "data" would exact-match
// before ambiguity against "database" could ever be seen. Rejecting the
// option set at the schema means the judge path never has to guess.
func TestAPrefixRelatedOptionSetIsRejected(t *testing.T) {
	qs := []ports.Question{{
		ID: "focus", Kind: ports.KindChoice, Ask: "?",
		Options: []string{"data", "database", "other"},
	}}
	_, err := app.AnswerSchema(qs)
	if err == nil {
		t.Fatal("an option set where one option is a prefix of another produced a schema")
	}
	if !strings.Contains(err.Error(), "data") || !strings.Contains(err.Error(), "database") {
		t.Errorf("error does not name both colliding strings: %v", err)
	}
	if !strings.Contains(err.Error(), "judge: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
}

// TestTwoOptionsSharingAFormIsRejected covers F1's second shape: two
// different options naming the identical form. Which option a matching
// answer resolves to would depend on Go's nondeterministic map iteration.
func TestTwoOptionsSharingAFormIsRejected(t *testing.T) {
	qs := []ports.Question{{
		ID: "focus", Kind: ports.KindChoice, Ask: "?",
		Options: []string{"backend", "frontend"},
		Forms:   map[string][]string{"backend": {"be", "server"}, "frontend": {"fe", "server"}},
	}}
	_, err := app.AnswerSchema(qs)
	if err == nil {
		t.Fatal("two options sharing an identical form produced a schema")
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("error does not name the shared form: %v", err)
	}
}

// TestShippedPackOptionSetsPassValidation proves the new guard does not
// reject the option sets a shipped pack's own choice question, and the live
// judge test's own choice and score questions, actually ask with.
func TestShippedPackOptionSetsPassValidation(t *testing.T) {
	qs := []ports.Question{
		{ID: "focus", Kind: ports.KindChoice, Ask: "?", Options: []string{"backend", "frontend", "platform", "data", "other"}},
		{ID: "warmth", Kind: ports.KindScore, Ask: "?", Options: []string{"freezing", "cool", "warm", "hot"}},
		{ID: "sky", Kind: ports.KindChoice, Ask: "?", Options: []string{"blue", "grey", "amber", "pink"}},
	}
	if _, err := app.AnswerSchema(qs); err != nil {
		t.Fatalf("a shipped pack's own option set was rejected: %v", err)
	}
}

// TestNoulDefaultsPassValidation proves the guard does not reject a noul's
// own default forms (yes/Yes/YES/true against no/No/NO/false).
func TestNoulDefaultsPassValidation(t *testing.T) {
	qs := []ports.Question{{ID: "clear", Kind: ports.KindNoul, Ask: "?"}}
	if _, err := app.AnswerSchema(qs); err != nil {
		t.Fatalf("the noul defaults were rejected: %v", err)
	}
}

// TestAnOptionSetSharingAFirstCharacterIsRejected covers the root cause a
// live engine exposed: "cold" and "cool" are not a prefix relation nor a
// shared form, so the older checks let them through, but any single
// truncated character ("c") is still a prefix of both. Naming a question's
// options in its prompt (docs/design/the-judge.md) makes the engine more
// likely to emit exactly such a truncation as a low-probability
// alternative, so the option set itself must be rejected up front rather
// than left for a live call to discover at random.
func TestAnOptionSetSharingAFirstCharacterIsRejected(t *testing.T) {
	qs := []ports.Question{{
		ID: "warmth", Kind: ports.KindScore, Ask: "?",
		Options: []string{"cold", "cool", "warm"},
	}}
	_, err := app.AnswerSchema(qs)
	if err == nil {
		t.Fatal("an option set where two options share a first character produced a schema")
	}
	if !strings.Contains(err.Error(), "warmth") {
		t.Errorf("error does not name the question: %v", err)
	}
	if !strings.Contains(err.Error(), "cold") || !strings.Contains(err.Error(), "cool") {
		t.Errorf("error does not name both colliding options: %v", err)
	}
}

// TestDistinctInitialOptionSetsPassValidation proves the new guard leaves a
// noul's own defaults and an ordinary distinct-initial option set alone.
func TestDistinctInitialOptionSetsPassValidation(t *testing.T) {
	qs := []ports.Question{
		{ID: "clear", Kind: ports.KindNoul, Ask: "?"},
		{ID: "length", Kind: ports.KindScore, Ask: "?", Options: []string{"short", "medium", "long"}},
	}
	if _, err := app.AnswerSchema(qs); err != nil {
		t.Fatalf("distinct-initial option sets were rejected: %v", err)
	}
}

// TestPropertiesAndRequiredAppearInDeclaredOrder proves the schema's
// property keys and required entries follow the order questions were
// given, not alphabetical order: a model whose grammar walks the fields in
// schema order answers zeta before alpha before mid, and each answer can
// then only depend on the ones before it. zeta, alpha and mid are chosen
// because alphabetical order would move zeta last.
func TestPropertiesAndRequiredAppearInDeclaredOrder(t *testing.T) {
	qs := []ports.Question{
		{ID: "zeta", Kind: ports.KindChoice, Ask: "?", Options: []string{"y", "n"}},
		{ID: "alpha", Kind: ports.KindChoice, Ask: "?", Options: []string{"y", "n"}},
		{ID: "mid", Kind: ports.KindChoice, Ask: "?", Options: []string{"y", "n"}},
	}
	b, err := app.AnswerSchema(qs)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := string(b)

	propsStart := strings.Index(s, `"properties"`)
	reqStart := strings.Index(s, `"required"`)
	if propsStart == -1 || reqStart == -1 {
		t.Fatalf("schema is missing properties or required: %s", s)
	}

	zetaProp := strings.Index(s[propsStart:], `"zeta"`)
	alphaProp := strings.Index(s[propsStart:], `"alpha"`)
	midProp := strings.Index(s[propsStart:], `"mid"`)
	if zetaProp == -1 || alphaProp == -1 || midProp == -1 {
		t.Fatalf("a property key is missing: %s", s)
	}
	if !(zetaProp < alphaProp && alphaProp < midProp) {
		t.Errorf("properties are not in declared order zeta, alpha, mid: %s", s)
	}

	zetaReq := strings.Index(s[reqStart:], `"zeta"`)
	alphaReq := strings.Index(s[reqStart:], `"alpha"`)
	midReq := strings.Index(s[reqStart:], `"mid"`)
	if zetaReq == -1 || alphaReq == -1 || midReq == -1 {
		t.Fatalf("a required entry is missing: %s", s)
	}
	if !(zetaReq < alphaReq && alphaReq < midReq) {
		t.Errorf("required is not in declared order zeta, alpha, mid: %s", s)
	}
}

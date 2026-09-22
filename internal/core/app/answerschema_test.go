package app_test

import (
	"encoding/json"
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

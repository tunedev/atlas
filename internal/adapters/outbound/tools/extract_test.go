package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

type stubExtractor struct {
	reply     string
	err       error
	gotText   string
	gotSchema string
}

func (s *stubExtractor) Extract(_ context.Context, text string, schema []byte) (json.RawMessage, error) {
	s.gotText, s.gotSchema = text, string(schema)
	return json.RawMessage(s.reply), s.err
}

func TestExtractRunPassesTextAndSchemaAndDecodesTheReply(t *testing.T) {
	e := &stubExtractor{reply: `{"sightings":[{"species":"wren","quote":"a wren"}]}`}
	out, err := tools.NewExtract(e).Invoke(context.Background(), map[string]string{
		"text": "a wren sang", "schema": `{"type":"object"}`,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if e.gotText != "a wren sang" || e.gotSchema != `{"type":"object"}` {
		t.Errorf("text = %q schema = %q", e.gotText, e.gotSchema)
	}
	fields := out.(map[string]any)["fields"].(map[string]any)
	if fields["sightings"].([]any)[0].(map[string]any)["species"] != "wren" {
		t.Errorf("fields = %v", fields)
	}
}

func TestExtractRunWrapsAnExtractorFailure(t *testing.T) {
	_, err := tools.NewExtract(&stubExtractor{err: errors.New("engine down")}).Invoke(context.Background(), map[string]string{
		"text": "a wren sang", "schema": `{"type":"object"}`,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "extract.run: ") {
		t.Errorf("err = %v", err)
	}
}

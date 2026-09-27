package mcpserve

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFlattenScalars(t *testing.T) {
	got, err := flatten(json.RawMessage(`{"city":"Oslo","days":3,"ratio":0.25,"big":12345678901234567890,"metric":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"city": "Oslo", "days": "3", "ratio": "0.25", "big": "12345678901234567890", "metric": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestFlattenEmpty(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`} {
		got, err := flatten(json.RawMessage(raw))
		if err != nil || len(got) != 0 {
			t.Errorf("flatten(%q) = %v, %v; want empty", raw, got, err)
		}
	}
}

func TestFlattenRejectsWhatHasNoStringForm(t *testing.T) {
	cases := map[string]string{
		`{"where":{"city":"Oslo"}}`: `"where"`,
		`{"days":[1,2]}`:            `"days"`,
		`{"city":null}`:             `"city"`,
		`["Oslo"]`:                  "object",
	}
	for raw, named := range cases {
		_, err := flatten(json.RawMessage(raw))
		if err == nil || !strings.Contains(err.Error(), named) {
			t.Errorf("flatten(%s) err = %v; want one naming %s", raw, err, named)
		}
	}
}

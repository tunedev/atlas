package search_test

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/tunedev/atlas/internal/core/search"
)

func resolve(t *testing.T, name string, data []byte) *jsonschema.Resolved {
	t.Helper()
	var s jsonschema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	rs, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("%s: resolve: %v", name, err)
	}
	return rs
}

// envelope resolves one of the embedded schemas.
func envelope(t *testing.T, name string) *jsonschema.Resolved {
	t.Helper()
	data, err := search.Schemas.ReadFile("schema/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return resolve(t, name, data)
}

func instance(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

const goodLoaf = `{
  "loaf": {"grain": "rye", "vegan": true, "toppings": ["sesame"]},
  "price": {"amount": 4.5, "currency": "EUR"},
  "open": {"hours": [7, 18]},
  "notes": "baked at dawn",
  "allergens": ["sesame"]
}`

func TestTheGeneratedSchemaAcceptsValidValues(t *testing.T) {
	rs := resolve(t, "generated", bakery(t).Schema())
	for name, values := range map[string]string{
		"every field stated": goodLoaf,
		"every field null": `{"loaf": {"grain": null, "vegan": null, "toppings": null},
		  "price": {"amount": null, "currency": null}, "open": {"hours": null},
		  "notes": null, "allergens": null}`,
	} {
		if err := rs.Validate(instance(t, []byte(values))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheGeneratedSchemaRejectsValuesOutsideTheDeclaration(t *testing.T) {
	rs := resolve(t, "generated", bakery(t).Schema())
	for name, edit := range map[string]func(map[string]any){
		"enum":          func(v map[string]any) { at(v, "loaf")["grain"] = "barley" },
		"pattern":       func(v map[string]any) { at(v, "price")["currency"] = "euro" },
		"canonical":     func(v map[string]any) { at(v, "loaf")["toppings"] = []any{"Sesame"} },
		"range bound":   func(v map[string]any) { at(v, "open")["hours"] = []any{7, 30} },
		"empty list":    func(v map[string]any) { v["allergens"] = []any{} },
		"wrong type":    func(v map[string]any) { at(v, "loaf")["vegan"] = "yes" },
		"missing field": func(v map[string]any) { delete(at(v, "price"), "amount") },
		"extra field":   func(v map[string]any) { at(v, "loaf")["crust"] = "crisp" },
	} {
		v := instance(t, []byte(goodLoaf)).(map[string]any)
		edit(v)
		if err := rs.Validate(v); err == nil {
			t.Errorf("%s: the generated schema accepted it", name)
		}
	}
}

func at(v map[string]any, key string) map[string]any { return v[key].(map[string]any) }

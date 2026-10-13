package search_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

// TestEveryPackWithExamplesMatchesThem checks each pack that declares search
// fields: its criteria example parses against them, its example enrichments
// fit both schemas, and Match gives its expected result.
func TestEveryPackWithExamplesMatchesThem(t *testing.T) {
	decls, err := filepath.Glob("../../../packs/*/fields.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) == 0 {
		t.Fatal("no pack declares search fields")
	}
	for _, decl := range decls {
		dir := filepath.Dir(decl)
		t.Run(filepath.Base(dir), func(t *testing.T) { checkPack(t, dir) })
	}
}

func checkPack(t *testing.T, dir string) {
	f, err := search.ParseFields(mustRead(t, filepath.Join(dir, "fields.json")))
	if err != nil {
		t.Fatalf("fields.json: %v", err)
	}
	criteriaDoc := mustRead(t, filepath.Join(dir, "criteria.example.json"))
	if err := envelope(t, "criteria.json").Validate(instance(t, criteriaDoc)); err != nil {
		t.Errorf("criteria.example.json against the criteria schema: %v", err)
	}
	c, err := search.ParseCriteria(criteriaDoc, f)
	if err != nil {
		t.Fatalf("criteria.example.json: %v", err)
	}

	paths, err := filepath.Glob(filepath.Join(dir, "examples", "enrichment-*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples/enrichment-*.json: %v", err)
	}
	doc, values := envelope(t, "enrichment.json"), resolve(t, "generated", f.Schema())
	var es []search.Enrichment
	for _, path := range paths {
		data := mustRead(t, path)
		if err := doc.Validate(instance(t, data)); err != nil {
			t.Errorf("%s against the enrichment schema: %v", path, err)
		}
		var e search.Enrichment
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := values.Validate(instance(t, mustMarshal(t, e.Values))); err != nil {
			t.Errorf("%s values against the generated schema: %v", path, err)
		}
		es = append(es, e)
	}

	got := mustMarshal(t, search.Match(c, es))
	if err := envelope(t, "result.json").Validate(instance(t, got)); err != nil {
		t.Errorf("Match output against the result schema: %v", err)
	}
	want := instance(t, mustRead(t, filepath.Join(dir, "examples", "result.json")))
	if !reflect.DeepEqual(instance(t, got), want) {
		t.Errorf("Match result differs from examples/result.json; got:\n%s", got)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

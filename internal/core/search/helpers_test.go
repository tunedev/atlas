package search_test

import (
	"os"
	"testing"

	"github.com/tunedev/atlas/internal/core/search"
)

// bakery loads the toy declaration the package's tests search over.
func bakery(t *testing.T) search.Fields {
	t.Helper()
	f, err := search.ParseFields(mustRead(t, "testdata/bakery-fields.json"))
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	return f
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestRenderItemUsesItsOwnDelimitersAndReplace(t *testing.T) {
	item := map[string]any{"id": "shelf/fiction/42", "title": "Dune", "pages": 412.0}
	got, err := app.RenderItem(`[[ replace .item.id "/" "~" ]] [[ .item.title ]] {{ untouched }} [[ .item.pages ]]`, item)
	if err != nil {
		t.Fatalf("RenderItem: %v", err)
	}
	if want := "shelf~fiction~42 Dune {{ untouched }} 412"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderItemFailsOnAMissingOrNullKey(t *testing.T) {
	for name, item := range map[string]any{
		"missing": map[string]any{"title": "Dune"},
		"null":    map[string]any{"title": "Dune", "author": nil},
	} {
		if _, err := app.RenderItem("[[ .item.author ]]", item); err == nil {
			t.Errorf("%s: rendered without error", name)
		}
	}
}

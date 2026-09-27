package app_test

import (
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestLineItemsMakesOneGroundedItemPerNonBlankLine(t *testing.T) {
	got := app.LineItems("  Can you trim a wick?  \r\n\n   \nCan you read a chart?")
	want := []any{
		map[string]any{"quote": "Can you trim a wick?", "status": "grounded"},
		map[string]any{"quote": "Can you read a chart?", "status": "grounded"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
	if got := app.LineItems(" \n\n"); len(got) != 0 || got == nil {
		t.Errorf("blank text gave %#v; want an empty, non-nil list", got)
	}
}

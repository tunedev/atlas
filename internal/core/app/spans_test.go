package app_test

import (
	"reflect"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestSpansSplitAtBulletsAndLines(t *testing.T) {
	log := "Kept the north light burning\n● Logged every passing ship by name and hour\n• Refitted the lamp   lens in winter\n\nok\n"
	got := app.Spans([]app.SpanSource{{Name: "log.txt", Text: log}}, 12)
	want := []app.Span{
		{ID: 0, Source: "log.txt", Text: "Kept the north light burning"},
		{ID: 1, Source: "log.txt", Text: "Logged every passing ship by name and hour"},
		{ID: 2, Source: "log.txt", Text: "Refitted the lamp lens in winter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSpansNumberAcrossSourcesInOrder(t *testing.T) {
	got := app.Spans([]app.SpanSource{
		{Name: "a.txt", Text: "first long enough line"},
		{Name: "b.txt", Text: "second long enough line"},
	}, 5)
	if len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 || got[1].Source != "b.txt" {
		t.Errorf("got %+v", got)
	}
	again := app.Spans([]app.SpanSource{
		{Name: "a.txt", Text: "first long enough line"},
		{Name: "b.txt", Text: "second long enough line"},
	}, 5)
	if !reflect.DeepEqual(got, again) {
		t.Error("the same inputs gave different spans")
	}
}

func TestSpanListingAndText(t *testing.T) {
	spans := []app.Span{{ID: 0, Text: "one line"}, {ID: 1, Text: "two line"}}
	if got := app.SpanListing(spans); got != "[0] one line\n[1] two line\n" {
		t.Errorf("listing %q", got)
	}
	if got := app.SpanText(spans); got != "one line\ntwo line\n" {
		t.Errorf("text %q", got)
	}
}

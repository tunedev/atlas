package crawlsource

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

const eventsTargets = `
- id: harbour
  url: https://library.example/events
  item: li.event
  key: link
  fields:
    name: {css: h3}
    room: {css: .room}
    link: {css: a, attr: href}
  static:
    venue: Harbour library
`

const eventsPage = `<html><body><h1>What's on</h1><ul>
<li class="event"><h3>Tide  talk</h3><span class="room">Harbour room</span><a href="/events/tide">more</a></li>
<li class="event"><h3>Knot workshop</h3><span class="room">Loft</span><a href="https://elsewhere.example/knots">more</a></li>
<li class="event"><h3>No link</h3></li>
</ul></body></html>`

func parse(t *testing.T, html string) *goquery.Selection {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return doc.Selection
}

func TestParseTargetsReadsAWellFormedList(t *testing.T) {
	ts, err := ParseTargets(eventsTargets)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ts) != 1 || ts[0].ID != "harbour" || ts[0].Fields["link"].Attr != "href" || ts[0].Static["venue"] != "Harbour library" {
		t.Errorf("targets = %+v", ts)
	}
	if ts[0].Render {
		t.Error("a target renders unless it says so")
	}
}

func TestParseTargetsRejectsWhatCannotBeCrawled(t *testing.T) {
	valid := "- id: a\n  url: https://x.example/\n  item: li\n  key: k\n  fields:\n    k: {css: a}\n"
	for name, raw := range map[string]string{
		"empty":            "",
		"not yaml":         "- id: [",
		"unknown key":      valid + "  headers: {Cookie: x}\n",
		"bad id":           strings.Replace(valid, "id: a", "id: A B", 1),
		"relative url":     strings.Replace(valid, "https://x.example/", "/jobs", 1),
		"non-http url":     strings.Replace(valid, "https://x.example/", "file:///etc/passwd", 1),
		"no item selector": strings.Replace(valid, "item: li", "item: \"\"", 1),
		"key not a field":  strings.Replace(valid, "key: k", "key: other", 1),
		"no fields":        "- id: a\n  url: https://x.example/\n  item: li\n  key: k\n",
		"duplicate id":     valid + strings.TrimPrefix(valid, ""),
		"static clash":     valid + "  static: {k: v}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTargets(raw); err == nil || !strings.HasPrefix(err.Error(), "crawlsource: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestExtractReadsEachItemAndResolvesLinks(t *testing.T) {
	ts, _ := ParseTargets(eventsTargets)
	base, _ := url.Parse("https://library.example/events")
	got := extract(parse(t, eventsPage), ts[0], base)
	if len(got) != 2 {
		t.Fatalf("items = %d, want 2 (the item with no key is dropped): %v", len(got), got)
	}
	first := got[0]
	if first["name"] != "Tide talk" || first["room"] != "Harbour room" || first["venue"] != "Harbour library" {
		t.Errorf("first = %v", first)
	}
	if first["link"] != "https://library.example/events/tide" {
		t.Errorf("link = %q; a relative href must resolve against the page", first["link"])
	}
	if got[1]["link"] != "https://elsewhere.example/knots" {
		t.Errorf("absolute link = %q", got[1]["link"])
	}
}

func TestAnEmptyPageSaysWhyItIsEmpty(t *testing.T) {
	changed := `<html><body><h1>What's on</h1>` + strings.Repeat("<p>This season the library hosts talks and workshops for all ages.</p>", 5) + `</body></html>`
	shell := `<html><head><script src="/bundle.js"></script></head><body><div id="root"></div><noscript>Enable JavaScript to see this page, which lists everything on this season.</noscript></body></html>`
	if k := emptyKind(parse(t, changed)); k != KindStale {
		t.Errorf("a text page with no matches = %q, want %q", k, KindStale)
	}
	if k := emptyKind(parse(t, shell)); k != KindNeedsRendering {
		t.Errorf("an application shell = %q, want %q", k, KindNeedsRendering)
	}
}

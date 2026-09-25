package crawlsource

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Failure kinds, one per reason a target yields nothing.
const (
	KindDisallowed           = "disallowed"
	KindFetch                = "fetch"
	KindStale                = "extraction_stale"
	KindNeedsRendering       = "needs_rendering"
	KindRenderingUnavailable = "rendering_unavailable"
)

// shellTextLimit is the visible text below which a page carrying script is
// taken for an application shell.
const shellTextLimit = 200

// extract reads one field map per element t.Item selects, adding t.Static.
// An element whose key field is empty is not an item.
func extract(page *goquery.Selection, t Target, base *url.URL) []map[string]string {
	var out []map[string]string
	page.Find(t.Item).Each(func(_ int, s *goquery.Selection) {
		fields := make(map[string]string, len(t.Fields)+len(t.Static))
		for name, f := range t.Fields {
			fields[name] = f.read(s, base)
		}
		for name, v := range t.Static {
			fields[name] = v
		}
		if fields[t.Key] != "" {
			out = append(out, fields)
		}
	})
	return out
}

func (f Field) read(item *goquery.Selection, base *url.URL) string {
	sel := item
	if f.CSS != "" {
		sel = item.Find(f.CSS).First()
	}
	if f.Attr == "" {
		return collapse(sel.Text())
	}
	v, _ := sel.Attr(f.Attr)
	v = strings.TrimSpace(v)
	if v != "" && (f.Attr == "href" || f.Attr == "src") {
		if ref, err := url.Parse(v); err == nil {
			return base.ResolveReference(ref).String()
		}
	}
	return v
}

// emptyKind says why a page yielded no item: a page carrying script with
// little visible text is an application shell that needs rendering; any
// other page no longer matches its target's rules.
func emptyKind(page *goquery.Selection) string {
	if page.Find("script").Length() > 0 && len(visibleText(page)) < shellTextLimit {
		return KindNeedsRendering
	}
	return KindStale
}

func visibleText(page *goquery.Selection) string {
	c := page.Clone()
	c.Find("script, style, noscript, template").Remove()
	return collapse(c.Text())
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

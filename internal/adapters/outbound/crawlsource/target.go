package crawlsource

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Target is one page to crawl and how to read it: Item selects each item,
// Fields reads each named value from inside an item, Static adds fixed
// values, and Key names the field that identifies an item. Render asks for
// the page to be rendered first; it takes effect only when rendering is on.
type Target struct {
	ID     string            `yaml:"id"`
	URL    string            `yaml:"url"`
	Item   string            `yaml:"item"`
	Key    string            `yaml:"key"`
	Fields map[string]Field  `yaml:"fields"`
	Static map[string]string `yaml:"static"`
	Render bool              `yaml:"render"`
}

// Field reads one value from an item: the text of the first element CSS
// selects (the item itself when CSS is empty), or its Attr attribute. An
// href or src is resolved against the page's URL.
type Field struct {
	CSS  string `yaml:"css"`
	Attr string `yaml:"attr"`
}

var targetID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParseTargets decodes a YAML list of targets strictly: an unknown key is an
// error, so a target cannot carry anything the crawler does not define, such
// as a header or a cookie.
func ParseTargets(raw string) ([]Target, error) {
	dec := yaml.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.KnownFields(true)
	var ts []Target
	if err := dec.Decode(&ts); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("crawlsource: parse targets: %w", err)
	}
	if len(ts) == 0 {
		return nil, errors.New("crawlsource: no targets")
	}
	seen := map[string]bool{}
	for _, t := range ts {
		if err := t.validate(); err != nil {
			return nil, fmt.Errorf("crawlsource: target %q: %w", t.ID, err)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("crawlsource: target id %q is used twice", t.ID)
		}
		seen[t.ID] = true
	}
	return ts, nil
}

func (t Target) validate() error {
	if !targetID.MatchString(t.ID) {
		return fmt.Errorf("id must match %s", targetID)
	}
	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q must be an absolute http or https URL", t.URL)
	}
	if u.User != nil {
		return errors.New("url must not carry credentials")
	}
	if t.Item == "" {
		return errors.New("no item selector")
	}
	if len(t.Fields) == 0 {
		return errors.New("no fields")
	}
	if _, ok := t.Fields[t.Key]; !ok {
		return fmt.Errorf("key %q is not one of its fields", t.Key)
	}
	for name := range t.Static {
		if _, ok := t.Fields[name]; ok {
			return fmt.Errorf("%q is both a field and a static value", name)
		}
	}
	return nil
}

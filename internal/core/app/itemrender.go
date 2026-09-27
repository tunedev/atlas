package app

import (
	"fmt"
	"strings"
	"text/template"
)

// RenderItem evaluates tmpl against one item, reachable as .item. Its
// delimiters are [[ and ]], so a step's own {{ }} has already been rendered
// by the runner and the two passes never meet. As with Render, a missing key
// or a null is an error rather than "<no value>". replace is
// strings.ReplaceAll: [[ replace .item.id "/" ":" ]].
func RenderItem(tmpl string, item any) (string, error) {
	t, err := template.New("item").
		Delims("[[", "]]").
		Option("missingkey=error").
		Funcs(template.FuncMap{"replace": strings.ReplaceAll}).
		Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("render: parse item template: %w", err)
	}
	data, err := sanitizeStepData(item, "item")
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	var out strings.Builder
	if err := t.Execute(&out, map[string]any{"item": data}); err != nil {
		return "", fmt.Errorf("render: item: %w", err)
	}
	return out.String(), nil
}

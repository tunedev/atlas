package mcpserve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// flatten turns a tool call's JSON arguments into the string map every
// ports.Tool takes. A scalar becomes its string form. A nested value or null
// is an error naming the argument, because it has no string form a tool
// could tell apart.
func flatten(raw json.RawMessage) (map[string]string, error) {
	with := map[string]string{}
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return with, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	for k, v := range args {
		switch v := v.(type) {
		case string:
			with[k] = v
		case json.Number:
			with[k] = v.String()
		case bool:
			with[k] = strconv.FormatBool(v)
		case nil:
			return nil, fmt.Errorf("argument %q is null", k)
		default:
			return nil, fmt.Errorf("argument %q is nested; only scalar arguments reach a tool", k)
		}
	}
	return with, nil
}

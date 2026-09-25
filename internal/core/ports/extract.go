package ports

import (
	"context"
	"encoding/json"
)

// Extractor turns text into a value shaped by schema, a JSON Schema. It
// states only what the text states; nothing about what the schema
// describes is known to it.
type Extractor interface {
	Extract(ctx context.Context, text string, schema []byte) (json.RawMessage, error)
}

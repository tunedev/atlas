package search

import "embed"

// Schemas holds the JSON Schemas of the three contracts: schema/criteria.json,
// schema/enrichment.json and schema/result.json.
//
//go:embed schema/*.json
var Schemas embed.FS

package openaiprov_test

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/core/app"
)

const liveLogText = `Ship's log, HMS Petrel.
1998-04: departed Plymouth for the Azores under Captain Ruth Hale.
- Weathered a force 9 gale off Finisterre with no damage.
- Charted two uncharted shoals near Terceira.
2001-09 to 2003: refit at Portsmouth; served as training vessel.
- Trained forty cadets in celestial navigation.`

const liveLogSchema = `{"type":"object","required":["voyages"],"additionalProperties":false,
"properties":{"voyages":{"type":"array","items":{"type":"object",
"required":["place","start","events","quote"],"additionalProperties":false,
"properties":{"place":{"type":"string"},
"start":{"type":"string","pattern":"^[0-9]{4}(-[0-9]{2})?$"},
"end":{"type":["string","null"],"pattern":"^[0-9]{4}(-[0-9]{2})?$"},
"quote":{"type":"string"},
"events":{"type":"array","items":{"type":"object","required":["text","quote"],
"additionalProperties":false,"properties":{"text":{"type":"string"},"quote":{"type":"string"}}}}}}}}}`

// TestExtractAgainstALiveEngine is skipped unless ATLAS_LIVE_PROVIDER is set.
// It proves a real engine honours a nested extraction schema, date pattern
// included, and reports how many quotes ground, since that number is what the
// increment note records.
func TestExtractAgainstALiveEngine(t *testing.T) {
	if os.Getenv("ATLAS_LIVE_PROVIDER") == "" {
		t.Skip("ATLAS_LIVE_PROVIDER not set")
	}
	baseURL := os.Getenv("ATLAS_LIVE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	model := os.Getenv("ATLAS_LIVE_MODEL")
	if model == "" {
		model = "qwen2.5-coder:7b"
	}
	p := openaiprov.New(openaiprov.Config{Name: model, BaseURL: baseURL, Model: model, Timeout: 5 * time.Minute, MaxBytes: 1 << 20})
	e := app.NewExtractor(p, app.ExtractorConfig{Temperature: 0, MaxTokens: 2048})

	raw, err := e.Extract(context.Background(), liveLogText, []byte(liveLogSchema))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var got struct {
		Voyages []struct {
			Start  string `json:"start"`
			Quote  string `json:"quote"`
			Events []struct {
				Quote string `json:"quote"`
			} `json:"events"`
		} `json:"voyages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("reply does not match the schema's shape: %v\n%s", err, raw)
	}
	if len(got.Voyages) == 0 {
		t.Fatalf("no voyages extracted:\n%s", raw)
	}
	date := regexp.MustCompile(`^[0-9]{4}(-[0-9]{2})?$`)
	for _, v := range got.Voyages {
		if !date.MatchString(v.Start) {
			t.Errorf("start %q breaks the schema's pattern", v.Start)
		}
	}
	ungrounded, err := app.Ground(raw, liveLogText)
	if err != nil {
		t.Fatalf("ground: %v", err)
	}
	t.Logf("reply:\n%s\nungrounded quotes: %v", raw, ungrounded)
}

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Fingerprint identifies everything a judgement of subject depended on:
// the rendered subject, the questions as asked, the rules they were checked
// against, the rule inputs those rules read (see RuleInputs), and the
// model. Equal fingerprints mean the same thing was asked of the same
// model against the same inputs, so the earlier answer stands.
func Fingerprint(subject string, qs []ports.Question, rules []Rule, inputs map[string]any, model string) (string, error) {
	h := sha256.New()
	err := json.NewEncoder(h).Encode(struct {
		Subject   string
		Questions []ports.Question
		Rules     []Rule
		Inputs    map[string]any
		Model     string
	}{subject, qs, rules, inputs, model})
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

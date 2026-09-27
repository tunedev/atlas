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
// against, and the model. Equal fingerprints mean the same thing was asked
// of the same model, so the earlier answer stands.
func Fingerprint(subject string, qs []ports.Question, rules []Rule, model string) (string, error) {
	h := sha256.New()
	err := json.NewEncoder(h).Encode(struct {
		Subject   string
		Questions []ports.Question
		Rules     []Rule
		Model     string
	}{subject, qs, rules, model})
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

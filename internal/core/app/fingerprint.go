package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Fingerprint identifies everything a judgement depended on: request (from
// JudgeRequest, the exact bytes a call to Ask sends the provider), the
// rules checked, the rule inputs those rules read (see RuleInputs), the id
// of the verdict question, and the model. Equal fingerprints mean the same
// request was asked of the same model against the same rules, inputs and
// verdict question, so the earlier answer stands. A changed prompt or
// schema changes request, so a prompt fix always re-judges rather than
// reusing a judgement it never actually asked.
func Fingerprint(request []byte, rules []Rule, inputs map[string]any, verdictID, model string) (string, error) {
	h := sha256.New()
	err := json.NewEncoder(h).Encode(struct {
		Request   json.RawMessage
		Rules     []Rule
		Inputs    map[string]any
		VerdictID string
		Model     string
	}{request, rules, inputs, verdictID, model})
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

package app

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/tunedev/atlas/internal/core/ports"
)

// PermissionRule decides requests whose tool name and kind both match. "*"
// matches any value.
type PermissionRule struct {
	ToolName string
	Kind     string
	Decision ports.PermissionDecision
}

// PermissionPolicy decides by the first matching rule. A request no rule
// matches, or one whose rule says ask, is put to human. It returns only allow
// or deny: a human error, or a human answer of ask, is a deny.
type PermissionPolicy struct {
	rules []PermissionRule
	human ports.Permission
}

func NewPermissionPolicy(rules []PermissionRule, human ports.Permission) *PermissionPolicy {
	return &PermissionPolicy{rules: rules, human: human}
}

func (p *PermissionPolicy) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	if d := p.match(req); d != ports.PermissionAsk {
		return d, nil
	}
	d, err := p.human.Decide(ctx, req)
	if err != nil {
		return ports.PermissionDeny, fmt.Errorf("permission: %w", err)
	}
	if d != ports.PermissionAllow {
		return ports.PermissionDeny, nil
	}
	return ports.PermissionAllow, nil
}

// match returns the first matching rule's decision, or ask when none match.
func (p *PermissionPolicy) match(req ports.PermissionRequest) ports.PermissionDecision {
	for _, r := range p.rules {
		if matches(r.ToolName, req.ToolName) && matches(r.Kind, req.Kind) {
			return r.Decision
		}
	}
	return ports.PermissionAsk
}

func matches(pattern, value string) bool { return pattern == "*" || pattern == value }

// BoundSummary returns s cut to at most budget bytes on a rune boundary,
// marked with "..." when anything was cut.
func BoundSummary(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

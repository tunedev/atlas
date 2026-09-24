package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// AgentSession points at a session an agent holds: its id, and which agent
// command produced it, since only that agent can resume it.
type AgentSession struct {
	SessionID       string
	Command         string
	Args            []string
	ProtocolVersion int
	CreatedAt       time.Time
}

// agentSessionDoc is an AgentSession as it is written to the record.
type agentSessionDoc struct {
	SessionID       string    `json:"session_id"`
	Command         string    `json:"command"`
	Args            []string  `json:"args"`
	ProtocolVersion int       `json:"protocol_version"`
	CreatedAt       time.Time `json:"created_at"`
}

// RecordAgentSession writes s as the session for subjectID and returns the
// path written. Recording the same session again is a no-op revision.
func RecordAgentSession(ctx context.Context, docs ports.Docs, subjectID string, s AgentSession) (string, error) {
	if subjectID == "" {
		return "", errors.New("agent session: subject id is empty")
	}
	if s.SessionID == "" {
		return "", errors.New("agent session: session id is empty")
	}
	body, err := json.MarshalIndent(agentSessionDoc(s), "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent session: encode: %w", err)
	}
	path := agentSessionPath(subjectID)
	if _, err := docs.Put(ctx, path, body, "Record agent session for "+subjectID); err != nil {
		return "", fmt.Errorf("agent session: put: %w", err)
	}
	return path, nil
}

// LoadAgentSession reads the session recorded for subjectID.
func LoadAgentSession(ctx context.Context, docs ports.Docs, subjectID string) (AgentSession, error) {
	body, err := docs.Get(ctx, agentSessionPath(subjectID))
	if err != nil {
		return AgentSession{}, fmt.Errorf("agent session: no session recorded for %s: %w", subjectID, err)
	}
	if len(body) == 0 {
		return AgentSession{}, fmt.Errorf("agent session: no session recorded for %s", subjectID)
	}
	var d agentSessionDoc
	if err := json.Unmarshal(body, &d); err != nil {
		return AgentSession{}, fmt.Errorf("agent session: decode %s: %w", subjectID, err)
	}
	return AgentSession(d), nil
}

func agentSessionPath(subjectID string) string {
	return "agent-sessions/" + subjectID + ".json"
}

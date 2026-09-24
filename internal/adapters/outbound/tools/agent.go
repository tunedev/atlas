package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// AgentSettings are the facts about the configured agent that a session
// pointer records, plus the defaults a step may leave out.
type AgentSettings struct {
	Command         string
	Args            []string
	ProtocolVersion int
	WorkDir         string
	Timeout         time.Duration
}

// Agent hands one turn of work to a ports.Agent, prints its tool calls and
// plans as they happen, and records the session it ran in.
type Agent struct {
	agent    ports.Agent
	docs     ports.Docs
	progress io.Writer
	s        AgentSettings
}

func NewAgent(a ports.Agent, docs ports.Docs, progress io.Writer, s AgentSettings) *Agent {
	return &Agent{agent: a, docs: docs, progress: progress, s: s}
}

func (t *Agent) Name() string { return "agent.do" }

func (t *Agent) Invoke(ctx context.Context, with map[string]string) (any, error) {
	subjectID := with["subject_id"]
	if subjectID == "" {
		return nil, fmt.Errorf("agent.do: no subject id")
	}
	if with["prompt"] == "" {
		return nil, fmt.Errorf("agent.do: no prompt")
	}
	workDir := with["workdir"]
	if workDir == "" {
		workDir = t.s.WorkDir
	}

	session := app.AgentSession{Command: t.s.Command, Args: t.s.Args, ProtocolVersion: t.s.ProtocolVersion, CreatedAt: time.Now().UTC()}
	if with["resume"] == "true" {
		prev, err := app.LoadAgentSession(ctx, t.docs, subjectID)
		if err != nil {
			return nil, fmt.Errorf("agent.do: %w", err)
		}
		if prev.Command != t.s.Command || !slices.Equal(prev.Args, t.s.Args) {
			return nil, fmt.Errorf("agent.do: session for %s belongs to %s %v, not the configured %s %v",
				subjectID, prev.Command, prev.Args, t.s.Command, t.s.Args)
		}
		if prev.ProtocolVersion != t.s.ProtocolVersion {
			return nil, fmt.Errorf("agent.do: session for %s was made with protocol version %d, not the configured agent's %d",
				subjectID, prev.ProtocolVersion, t.s.ProtocolVersion)
		}
		session = prev
	}

	turnCtx, cancel := context.WithTimeout(ctx, t.s.Timeout)
	defer cancel()
	res, sessionID, err := t.agent.Do(turnCtx, ports.AgentTask{Prompt: with["prompt"], WorkDir: workDir}, session.SessionID, t.report)
	if err != nil {
		return nil, t.recordFailedTurn(ctx, subjectID, session, sessionID, err)
	}

	session.SessionID = sessionID
	path, err := app.RecordAgentSession(ctx, t.docs, subjectID, session)
	if err != nil {
		return nil, fmt.Errorf("agent.do: %w", err)
	}
	return map[string]any{
		"text":        res.Text,
		"stop_reason": res.StopReason,
		"session_id":  sessionID,
		"unverified":  res.Unverified,
		"path":        path,
	}, nil
}

// recordFailedTurn keeps the pointer to a session the failed turn ran in,
// so the work can be resumed, and returns doErr joined with any failure to
// record it. ctx is the caller's, not the turn's, so a turn that ran out of
// time still leaves a pointer.
func (t *Agent) recordFailedTurn(ctx context.Context, subjectID string, session app.AgentSession, sessionID string, doErr error) error {
	doErr = fmt.Errorf("agent.do: %w", doErr)
	if sessionID == "" {
		return doErr
	}
	session.SessionID = sessionID
	if _, err := app.RecordAgentSession(ctx, t.docs, subjectID, session); err != nil {
		return errors.Join(doErr, fmt.Errorf("agent.do: record session %s: %w", sessionID, err))
	}
	return doErr
}

// report prints tool calls and plans as they happen, with control text
// escaped. Message text arrives in fragments and is returned whole in the
// result instead.
func (t *Agent) report(ev ports.AgentEvent) {
	if ev.Kind == ports.AgentEventMessage {
		return
	}
	fmt.Fprintf(t.progress, "agent %s: %s\n", ev.Kind, graphic(ev.Text))
}

// graphic escapes every non-printable rune in s, so text from the agent
// cannot move the cursor, clear a line or start a new one.
func graphic(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

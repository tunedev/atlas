package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/domain"
)

// Acknowledge records the user's consent to send the loaded views'
// disclosed classes to one endpoint of the egress table. It commits the
// record through the runner like any other write.
func (s *Server) Acknowledge(ctx context.Context, req *connect.Request[uiv1.AcknowledgeRequest]) (*connect.Response[uiv1.AcknowledgeResponse], error) {
	endpoint := req.Msg.Endpoint
	if !slices.ContainsFunc(s.deps.Egress, func(e Endpoint) bool { return e.Endpoint == endpoint }) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("web: %q is not in the egress table", endpoint))
	}
	bp, err := acknowledgement(endpoint, s.discloses(), time.Now().UTC())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if _, err := s.runInternal(ctx, bp); err != nil {
		return nil, connect.NewError(connect.CodeUnknown, err)
	}
	return connect.NewResponse(&uiv1.AcknowledgeResponse{}), nil
}

// unacknowledged returns the hosted endpoints with no acknowledgement on
// record that covers every class the loaded views disclose.
func (s *Server) unacknowledged(ctx context.Context) ([]Endpoint, error) {
	var hosted []Endpoint
	for _, e := range s.deps.Egress {
		if e.Hosted {
			hosted = append(hosted, e)
		}
	}
	if len(hosted) == 0 {
		return nil, nil
	}
	bp, err := findAcknowledgements(hosted)
	if err != nil {
		return nil, err
	}
	state, err := s.runInternal(ctx, bp)
	if err != nil {
		return nil, err
	}
	discloses := s.discloses()
	var out []Endpoint
	for i, e := range hosted {
		ok, err := covers(state.Outputs()[findStep(i)], discloses)
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// needsAcknowledgement is the gate's refusal for endpoints.
func (s *Server) needsAcknowledgement(endpoints []Endpoint) *uiv1.RunResponse {
	names := make([]string, len(endpoints))
	for i, e := range endpoints {
		names[i] = e.Endpoint
	}
	msg := &uiv1.NeedsAcknowledgement{Endpoints: names, Discloses: s.discloses()}
	return &uiv1.RunResponse{Event: &uiv1.RunResponse_NeedsAcknowledgement{NeedsAcknowledgement: msg}}
}

// discloses is the union of every loaded view's disclosed classes, in
// first-seen order.
func (s *Server) discloses() []string {
	var out []string
	for _, v := range s.deps.Views {
		for _, class := range v.Discloses {
			if !slices.Contains(out, class) {
				out = append(out, class)
			}
		}
	}
	return out
}

// runInternal runs a blueprint the server builds itself. It takes the run
// slot but streams nothing and passes no gate.
func (s *Server) runInternal(ctx context.Context, bp domain.Blueprint) (*domain.State, error) {
	release, err := s.hold(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RunTimeout)
	defer cancel()
	state, err := s.deps.Runner.Run(ctx, bp)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return state, nil
}

func findStep(i int) string { return "find" + strconv.Itoa(i) }

// findAcknowledgements looks up each endpoint's acknowledgement rows, one
// index.find step per endpoint. Values travel as vars so no template reads
// them.
func findAcknowledgements(endpoints []Endpoint) (domain.Blueprint, error) {
	bp := domain.Blueprint{Name: "egress.find", Vars: map[string]string{}}
	for i, e := range endpoints {
		match, err := yaml.Marshal(map[string]string{"endpoint": e.Endpoint})
		if err != nil {
			return domain.Blueprint{}, fmt.Errorf("web: %w", err)
		}
		id := findStep(i)
		bp.Vars[id] = string(match)
		bp.Steps = append(bp.Steps, domain.Step{ID: id, Tool: "index.find", With: map[string]string{
			"kind":  "egress",
			"match": "{{.vars." + id + "}}",
		}})
	}
	return bp, nil
}

// covers reports whether any index.find row in found records every class
// in discloses.
func covers(found any, discloses []string) (bool, error) {
	raw, err := json.Marshal(found)
	if err != nil {
		return false, fmt.Errorf("web: %w", err)
	}
	var result struct {
		Rows []struct {
			Fields map[string]string `json:"fields"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, fmt.Errorf("web: index.find result: %w", err)
	}
	for _, row := range result.Rows {
		var recorded []string
		if err := json.Unmarshal([]byte(row.Fields["discloses"]), &recorded); err != nil {
			continue
		}
		if !slices.ContainsFunc(discloses, func(c string) bool { return !slices.Contains(recorded, c) }) {
			return true, nil
		}
	}
	return false, nil
}

// acknowledgement is the one-step blueprint that records consent for
// endpoint at atlas/egress/<first 16 hex of sha256(endpoint)>.json.
func acknowledgement(endpoint string, discloses []string, when time.Time) (domain.Blueprint, error) {
	if discloses == nil {
		discloses = []string{}
	}
	body, err := json.Marshal(struct {
		Endpoint  string   `json:"endpoint"`
		Discloses []string `json:"discloses"`
		When      string   `json:"when"`
	}{endpoint, discloses, when.Format(time.RFC3339)})
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("web: %w", err)
	}
	classes, err := json.Marshal(discloses)
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("web: %w", err)
	}
	fields, err := yaml.Marshal(map[string]string{"endpoint": endpoint, "discloses": string(classes)})
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("web: %w", err)
	}
	sum := sha256.Sum256([]byte(endpoint))
	return domain.Blueprint{
		Name: "egress.acknowledge",
		Vars: map[string]string{
			"path":    "atlas/egress/" + hex.EncodeToString(sum[:])[:16] + ".json",
			"body":    string(body),
			"fields":  string(fields),
			"message": "Acknowledge data sent to " + endpoint,
		},
		Steps: []domain.Step{{ID: "acknowledge", Tool: "docs.put", With: map[string]string{
			"path":    "{{.vars.path}}",
			"body":    "{{.vars.body}}",
			"kind":    "egress",
			"fields":  "{{.vars.fields}}",
			"message": "{{.vars.message}}",
		}}},
	}, nil
}

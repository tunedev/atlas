package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
)

// record is one document docs.put wrote, as the index sees it.
type record struct {
	kind   string
	fields map[string]string
	body   string
}

// recordTool is an in-memory docs.put or index.find over reg.records.
type recordTool struct {
	name string
	reg  *fakeRegistry
}

func (t recordTool) Name() string { return t.name }

func (t recordTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	fields := map[string]string{}
	src := with["match"]
	if t.name == "docs.put" {
		src = with["fields"]
	}
	if err := yaml.Unmarshal([]byte(src), &fields); err != nil {
		return nil, err
	}
	t.reg.mu.Lock()
	defer t.reg.mu.Unlock()
	if t.name == "docs.put" {
		if t.reg.records == nil {
			t.reg.records = map[string]record{}
		}
		t.reg.records[with["path"]] = record{kind: with["kind"], fields: fields, body: with["body"]}
		return map[string]any{"path": with["path"], "rev": "r1"}, nil
	}
	rows := []map[string]any{}
	for path, r := range t.reg.records {
		if r.kind == with["kind"] && matches(r.fields, fields) {
			rows = append(rows, map[string]any{"path": path, "kind": r.kind, "fields": r.fields})
		}
	}
	return map[string]any{"rows": rows}, nil
}

func matches(fields, match map[string]string) bool {
	for k, v := range match {
		if fields[k] != v {
			return false
		}
	}
	return true
}

func (r *fakeRegistry) stored() map[string]record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.records)
}

const (
	oneClass   = "discloses: [shelf notes]\n" + shelfView
	twoClasses = "discloses: [shelf notes, drawer notes]\n" + shelfView
	hostedAt   = "https://api.example.com"
)

func hosted(endpoint string) []web.Endpoint {
	return []web.Endpoint{{Endpoint: endpoint, Hosted: true, Tools: []string{"echo"}}}
}

var homeRun = &uiv1.RunRequest{View: "shelf", Screen: "home", Action: -1, Params: map[string]string{"label": "oak"}}

// runGated runs the home screen and reports the NeedsAcknowledgement it
// ended with, or nil when the run reached Done.
func runGated(t *testing.T, c uiv1.UIServiceClient, origin string) *uiv1.NeedsAcknowledgement {
	t.Helper()
	events, err := collect(t, c, origin, homeRun)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := events[len(events)-1]
	if needs := last.GetNeedsAcknowledgement(); needs != nil {
		if len(events) != 1 {
			t.Errorf("events = %v, want NeedsAcknowledgement alone", events)
		}
		return needs
	}
	if last.GetDone() == nil {
		t.Fatalf("last event = %v, want Done or NeedsAcknowledgement", last)
	}
	return nil
}

func acknowledged(t *testing.T, c uiv1.UIServiceClient, origin string) bool {
	t.Helper()
	resp, err := c.Views(context.Background(), withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatalf("Views: %v", err)
	}
	return resp.Msg.Egress[0].Acknowledged
}

func acknowledge(t *testing.T, c uiv1.UIServiceClient, origin, endpoint string) error {
	t.Helper()
	_, err := c.Acknowledge(context.Background(), withOrigin(&uiv1.AcknowledgeRequest{Endpoint: endpoint}, origin))
	return err
}

func TestHostedEndpointNeedsAcknowledgement(t *testing.T) {
	reg := newFakeRegistry(t, nil)
	c, origin := serveView(t, runConfig(), oneClass, reg, web.Deps{Egress: hosted(hostedAt)})

	needs := runGated(t, c, origin)
	if needs == nil || !slices.Equal(needs.Endpoints, []string{hostedAt}) || !slices.Equal(needs.Discloses, []string{"shelf notes"}) {
		t.Fatalf("before acknowledging: %v, want NeedsAcknowledgement for %s disclosing [shelf notes]", needs, hostedAt)
	}
	if calls := reg.invoked(); len(calls) != 0 {
		t.Fatalf("view tools ran before acknowledgement: %v", calls)
	}
	if acknowledged(t, c, origin) {
		t.Error("Views reports the endpoint acknowledged before Acknowledge")
	}

	if err := acknowledge(t, c, origin, "https://elsewhere.example.com"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("acknowledging an endpoint not in the table: err = %v, want InvalidArgument", err)
	}
	if err := acknowledge(t, c, origin, hostedAt); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	assertAcknowledgementRecord(t, reg, hostedAt, []string{"shelf notes"})

	if needs := runGated(t, c, origin); needs != nil {
		t.Fatalf("after acknowledging: %v, want the run to proceed", needs)
	}
	if calls := reg.invoked(); !slices.Equal(calls, []string{"echo"}) {
		t.Errorf("view tools after acknowledgement = %v, want [echo]", calls)
	}
	if !acknowledged(t, c, origin) {
		t.Error("Views reports the endpoint unacknowledged after Acknowledge")
	}

	wider, wOrigin := serveView(t, runConfig(), twoClasses, reg, web.Deps{Egress: hosted(hostedAt)})
	if needs := runGated(t, wider, wOrigin); needs == nil || !slices.Equal(needs.Discloses, []string{"shelf notes", "drawer notes"}) {
		t.Errorf("with a class added: %v, want NeedsAcknowledgement disclosing both classes", needs)
	}

	moved, mOrigin := serveView(t, runConfig(), oneClass, reg, web.Deps{Egress: hosted(hostedAt + ":8443")})
	if needs := runGated(t, moved, mOrigin); needs == nil || !slices.Equal(needs.Endpoints, []string{hostedAt + ":8443"}) {
		t.Errorf("with the endpoint changed: %v, want NeedsAcknowledgement for the new endpoint", needs)
	}
}

func TestLocalEndpointNeverAsks(t *testing.T) {
	reg := newFakeRegistry(t, nil)
	local := []web.Endpoint{{Endpoint: "http://localhost:11434", Hosted: false, Tools: []string{"echo"}}}
	c, origin := serveView(t, runConfig(), oneClass, reg, web.Deps{Egress: local})

	if needs := runGated(t, c, origin); needs != nil {
		t.Fatalf("local endpoint: %v, want the run to proceed", needs)
	}
	if calls := reg.invoked(); !slices.Equal(calls, []string{"echo"}) {
		t.Errorf("view tools = %v, want [echo]", calls)
	}
}

// assertAcknowledgementRecord checks the record Acknowledge wrote for
// endpoint: its path, kind, index fields and body.
func assertAcknowledgementRecord(t *testing.T, reg *fakeRegistry, endpoint string, discloses []string) {
	t.Helper()
	sum := sha256.Sum256([]byte(endpoint))
	path := "atlas/egress/" + hex.EncodeToString(sum[:])[:16] + ".json"
	r, ok := reg.stored()[path]
	if !ok {
		t.Fatalf("no record at %s; have %v", path, reg.stored())
	}
	wantDiscloses, _ := json.Marshal(discloses)
	if r.kind != "egress" || r.fields["endpoint"] != endpoint || r.fields["discloses"] != string(wantDiscloses) {
		t.Errorf("record = %+v, want kind egress, endpoint %s, discloses %s", r, endpoint, wantDiscloses)
	}
	var body struct {
		Endpoint  string   `json:"endpoint"`
		Discloses []string `json:"discloses"`
		When      string   `json:"when"`
	}
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
	if body.Endpoint != endpoint || !slices.Equal(body.Discloses, discloses) || body.When == "" {
		t.Errorf("body = %+v", body)
	}
}

func TestGateAnswersWithoutTheRunSlot(t *testing.T) {
	reg := newFakeRegistry(t, nil)
	c, origin := serveView(t, runConfig(), oneClass, reg, web.Deps{Egress: hosted(hostedAt)})
	if err := acknowledge(t, c, origin, hostedAt); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	slow, err := c.Run(context.Background(), withOrigin(&uiv1.RunRequest{View: "shelf", Screen: "wait", Action: -1}, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	for slow.Receive() {
		if s := slow.Msg().GetStep(); s != nil && s.Status == "started" {
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := c.Views(ctx, withOrigin(&uiv1.ViewsRequest{}, origin))
	if err != nil {
		t.Fatalf("Views while a run holds the slot: %v", err)
	}
	if !resp.Msg.Egress[0].Acknowledged {
		t.Error("Views reports the endpoint unacknowledged")
	}

	second, err := c.Run(ctx, withOrigin(homeRun, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !second.Receive() {
		t.Fatalf("second run ended: %v", second.Err())
	}
	if q := second.Msg().GetQueued(); q == nil || q.Ahead != 1 {
		t.Errorf("second run's first event = %v, want Queued{ahead: 1}", second.Msg())
	}
}

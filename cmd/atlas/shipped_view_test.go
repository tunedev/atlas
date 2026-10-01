package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
)

// cannedJudgeReply is a chat completion answering the verdict question
// "apply", with the per-token alternatives the judge reads its mass from.
const cannedJudgeReply = `{
 "model": "a-model",
 "usage": {"prompt_tokens": 10, "completion_tokens": 3},
 "choices": [{
   "message": {"content": "{\"verdict\": \"apply\"}"},
   "logprobs": {"content": [
     {"token": "{\"verdict\": \"", "logprob": 0, "top_logprobs": []},
     {"token": "apply", "logprob": -0.1,
      "top_logprobs": [{"token": "apply", "logprob": -0.1}, {"token": "skip", "logprob": -2.5}]},
     {"token": "\"}", "logprob": 0, "top_logprobs": []}
   ]}
 }]
}`

// cannedModel is a loopback model endpoint that answers every request with
// cannedJudgeReply.
func cannedModel(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cannedJudgeReply)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/v1"
}

const seedView = `title: Seed
screens:
  - id: judge
    title: Judge
    params: [subject_id]
    run: seed.yaml
    vars: {subject_id: param.subject_id}
`

const seedPack = `name: seed
vars:
  subject_id: ""
steps:
  - id: judged
    tool: judge.ask
    with:
      subject_id: "{{ .vars.subject_id }}"
      subject: A shelf of logbooks, west wing.
      questions: |
        - id: verdict
          type: choice
          ask: Worth acting on?
          options: [apply, reach, skip]
`

// shippedView returns the path and id of the one shipped view file.
func shippedView(t *testing.T) (string, string) {
	t.Helper()
	paths, err := filepath.Glob("../../packs/*.ui.yaml")
	if err != nil || len(paths) != 1 {
		t.Fatalf("shipped views = %v, %v; want one", paths, err)
	}
	return paths[0], strings.TrimSuffix(filepath.Base(paths[0]), ".ui.yaml")
}

// doneState runs req to Done and decodes its state into v.
func doneState(t *testing.T, c uiv1.UIServiceClient, origin string, req *uiv1.RunRequest, v any) {
	t.Helper()
	events := runToEnd(t, c, origin, req)
	done := events[len(events)-1].GetDone()
	if done == nil {
		t.Fatalf("%s/%d ended with %v; want Done", req.Screen, req.Action, events[len(events)-1])
	}
	if err := json.Unmarshal([]byte(done.StateJson), v); err != nil {
		t.Fatalf("state %s: %v", done.StateJson, err)
	}
}

// The shipped view's queue lists a recorded judgement, its review screen
// shows the verdict, and Approve records a decision that agrees with it.
func TestShippedViewApprovesFromTheQueue(t *testing.T) {
	cfg := serveConfig(t, cannedModel(t))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seed.ui.yaml"), []byte(seedView), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.yaml"), []byte(seedPack), 0o600); err != nil {
		t.Fatal(err)
	}
	shipped, id := shippedView(t)
	cfg.Web.Views = append(cfg.Web.Views, filepath.Join(dir, "seed.ui.yaml"), shipped)
	c, origin, _ := startServe(t, cfg)
	subject := map[string]string{"subject_id": "s-1"}

	var seeded struct {
		Judged struct{ Path string } `json:"judged"`
	}
	doneState(t, c, origin, &uiv1.RunRequest{View: "seed", Screen: "judge", Action: -1, Params: subject}, &seeded)

	var queue struct {
		Judgements struct {
			Rows []struct {
				Path   string            `json:"path"`
				Fields map[string]string `json:"fields"`
			} `json:"rows"`
		} `json:"judgements"`
	}
	doneState(t, c, origin, &uiv1.RunRequest{View: id, Screen: "queue", Action: -1}, &queue)
	rows := queue.Judgements.Rows
	if len(rows) != 1 || rows[0].Fields["subject_id"] != "s-1" || rows[0].Path != seeded.Judged.Path {
		t.Fatalf("queue rows = %+v; want the judgement at %s", rows, seeded.Judged.Path)
	}

	var review struct {
		Assessment struct{ Verdict string } `json:"assessment"`
	}
	doneState(t, c, origin, &uiv1.RunRequest{View: id, Screen: "review", Action: -1, Params: subject}, &review)
	if review.Assessment.Verdict != "apply" {
		t.Fatalf("review verdict = %q; want apply", review.Assessment.Verdict)
	}

	approve := &uiv1.RunRequest{View: id, Screen: "review", Action: 0, Params: subject}
	if events := runToEnd(t, c, origin, approve); events[len(events)-1].GetDone() == nil {
		t.Fatalf("Approve ended with %v; want Done", events[len(events)-1])
	}

	decisions := decisionRows(t, c, origin)
	if len(decisions) != 1 {
		t.Fatalf("decision rows = %v; want one", decisions)
	}
	d := decisions[0]
	if d["subject_id"] != "s-1" || d["decision"] != "apply" || d["verdict_at_decision"] != "apply" || d["agrees"] != "true" {
		t.Errorf("decision row = %v; want s-1 apply, verdict apply, agrees true", d)
	}
}

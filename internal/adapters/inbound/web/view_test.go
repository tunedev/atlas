package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/domain"
)

// fakeLoader parses nothing; it returns a fixed blueprint keyed by the
// pack file's base name, or an error for any other name.
func fakeLoader(path string) (domain.Blueprint, error) {
	switch strings.TrimSuffix(filepath.Base(path), ".yaml") {
	case "days":
		return domain.Blueprint{Name: "days", Vars: map[string]string{"port": ""}}, nil
	case "note":
		return domain.Blueprint{Name: "note", Vars: map[string]string{"day": "", "text": "", "path": ""}}, nil
	default:
		return domain.Blueprint{}, fmt.Errorf("no pack at %s", path)
	}
}

func TestLoadViewsAcceptsTheFixture(t *testing.T) {
	views, err := LoadViews([]string{"testdata/logbook.ui.yaml"}, fakeLoader)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("got %d views, want 1", len(views))
	}
	v := views[0]
	if v.ID != "logbook" {
		t.Errorf("ID = %q, want logbook", v.ID)
	}
	if v.Title != "Logbook" {
		t.Errorf("Title = %q, want Logbook", v.Title)
	}
	if len(v.Screens) != 2 {
		t.Fatalf("got %d screens, want 2", len(v.Screens))
	}
	days, day := v.Screens[0], v.Screens[1]
	if days.ID != "days" || day.ID != "day" {
		t.Fatalf("screen ids = %q, %q, want days, day", days.ID, day.ID)
	}
	if len(days.Show) != 1 || days.Show[0].Kind != "table" {
		t.Fatalf("days.Show = %+v, want one table widget", days.Show)
	}
	if len(day.Actions) != 2 || len(day.Actions[0].Input) != 2 {
		t.Fatalf("day.Actions = %+v, want two actions, the first with two inputs", day.Actions)
	}
	if day.Actions[1].Label != "Archive" || day.Actions[1].Input == nil || len(day.Actions[1].Input) != 0 {
		t.Fatalf("day.Actions[1] = %+v, want Archive with a non-nil, empty Input", day.Actions[1])
	}
}

// TestLoadedViewsMarshalWithNoNullSlices proves that every slice field in the
// loaded views survives to JSON as [] rather than null, wherever the source
// YAML omits it: days has no actions or params, and day's Archive action has
// no input. Screen.svelte reads these as arrays unconditionally.
func TestLoadedViewsMarshalWithNoNullSlices(t *testing.T) {
	views, err := LoadViews([]string{"testdata/logbook.ui.yaml"}, fakeLoader)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}

	b, err := json.Marshal(views)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if strings.Contains(string(b), "null") {
		t.Errorf("views_json contains null: %s", b)
	}
}

func TestLoadViewsRejects(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "an unknown widget",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: const.shelf}
    show:
      - frobnicate: {from: rows}
`,
		},
		{
			name: "a bad open",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: const.shelf}
    show:
      - table: {from: rows, open: {screen: nope, param: {day: day}}}
`,
		},
		{
			name: "a typo in a widget's spec",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: const.shelf}
    show:
      - table: {form: rows}
`,
		},
		{
			name: "a typo in a widget's open",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: const.shelf}
    show:
      - table: {from: rows, open: {screen: days, parm: {day: day}}}
`,
		},
		{
			name: "a var the pack does not declare",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {bogus: const.x}
`,
		},
		{
			name: "input. in a screen",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: input.whatever}
`,
		},
		{
			name: "state. in a screen",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: state.rows}
`,
		},
		{
			name: "an unknown server. name",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: server.bogus_root}
`,
		},
		{
			name: "an undeclared param.",
			yaml: `
title: Logbook
screens:
  - id: days
    params: [x]
    run: days.yaml
    vars: {port: param.y}
`,
		},
		{
			name: "a duplicate id",
			yaml: `
title: Logbook
screens:
  - id: days
    run: days.yaml
    vars: {port: const.a}
  - id: days
    run: days.yaml
    vars: {port: const.b}
`,
		},
		{
			name: "a run the loader fails",
			yaml: `
title: Logbook
screens:
  - id: days
    run: missing.yaml
`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "logbook.ui.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadViews([]string{path}, fakeLoader)
			if err == nil {
				t.Fatal("LoadViews: got nil error, want one")
			}
			if !strings.HasPrefix(err.Error(), "web: ") {
				t.Errorf("error %q does not start with %q", err.Error(), "web: ")
			}
		})
	}
}

func TestResolveBindsEachSource(t *testing.T) {
	b := bindings{
		params: map[string]string{"day": "monday"},
		inputs: map[string]string{"text": "did the thing"},
		server: map[string]string{"store_root": "/data"},
		state: map[string]any{
			"row":   map[string]any{"path": "notes/monday.md"},
			"count": 3.0,
		},
	}
	vars := map[string]string{
		"day":   "param.day",
		"text":  "input.text",
		"root":  "server.store_root",
		"greet": "const.hello",
		"path":  "state.row.path",
		"count": "state.count",
	}

	got, err := resolve(vars, b)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := map[string]string{
		"day":   "monday",
		"text":  "did the thing",
		"root":  "/data",
		"greet": "hello",
		"path":  "notes/monday.md",
		"count": "3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolve() = %v, want %v", got, want)
	}
}

func TestResolveRefusesAMissingValue(t *testing.T) {
	b := bindings{params: map[string]string{}}
	if _, err := resolve(map[string]string{"day": "param.day"}, b); err == nil {
		t.Fatal("resolve: want an error for a missing param value")
	}
}

func TestCheckInputsRefusesAnOffListOption(t *testing.T) {
	a := Action{Input: []Input{{Name: "mood", Options: []string{"calm", "rough"}}}}
	if err := a.checkInputs(map[string]string{"mood": "furious"}); err == nil {
		t.Fatal("checkInputs: want an error for an off-list option")
	}
	if err := a.checkInputs(map[string]string{"mood": "calm"}); err != nil {
		t.Fatalf("checkInputs: %v", err)
	}
}

func TestLookupWalksMapsAndIndexes(t *testing.T) {
	v := map[string]any{
		"rows": []any{
			map[string]any{"path": "a.md"},
			map[string]any{"path": "b.md"},
		},
	}

	got, ok := lookup(v, "rows.1.path")
	if !ok || got != "b.md" {
		t.Fatalf("lookup(rows.1.path) = %v, %v, want b.md, true", got, ok)
	}
	if _, ok := lookup(v, "rows.9.path"); ok {
		t.Fatal("lookup: want false for an out-of-range index")
	}
	if _, ok := lookup(v, "missing"); ok {
		t.Fatal("lookup: want false for a missing key")
	}
}

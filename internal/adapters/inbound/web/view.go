// Package web is the inbound HTTP adapter for the web UI. This file loads
// pack-declared view files: named groups of screens, each backed by a pack,
// validated against that pack's declared vars at load time so a bad view
// fails at boot rather than on a click.
package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/domain"
)

// Loader reads a pack file into a blueprint; the composition root passes
// packfile.Load.
type Loader func(path string) (domain.Blueprint, error)

// View is one pack-declared screen file: a named group of screens sharing a
// directory.
type View struct {
	ID        string   `json:"id" yaml:"-"` // the file's base name without ".ui.yaml"
	Title     string   `json:"title" yaml:"title"`
	Discloses []string `json:"discloses" yaml:"discloses"`
	Screens   []Screen `json:"screens" yaml:"screens"`
	dir       string
}

// Screen is one page of a view. Run and Vars drive how the server fetches
// the screen's state; they never reach the browser.
type Screen struct {
	ID      string            `json:"id" yaml:"id"`
	Title   string            `json:"title" yaml:"title"`
	Params  []string          `json:"params" yaml:"params"`
	Run     string            `json:"-" yaml:"run"`
	Vars    map[string]string `json:"-" yaml:"vars"`
	Show    []Widget          `json:"show" yaml:"show"`
	Actions []Action          `json:"actions" yaml:"actions"`
}

// Action is a screen's form submit: a pack run against the screen's params,
// its own inputs, and the screen's last state.
type Action struct {
	Label string            `json:"label" yaml:"label"`
	Run   string            `json:"-" yaml:"run"`
	Input []Input           `json:"input" yaml:"input"`
	Vars  map[string]string `json:"-" yaml:"vars"`
}

// Input is one field an action's form collects. Options makes it a closed
// choice; otherwise Text marks it as free text.
type Input struct {
	Name    string   `json:"name" yaml:"name"`
	Options []string `json:"options,omitempty" yaml:"options,omitempty"`
	Text    bool     `json:"text,omitempty" yaml:"text,omitempty"`
}

// Widget is one element of a screen's Show list. It decodes from a
// single-key YAML map: the key becomes Kind, the value is its spec.
type Widget struct {
	Kind    string   `json:"kind"`
	From    string   `json:"from"`
	Columns []string `json:"columns,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Open    *Open    `json:"open,omitempty"`
}

// Open is a widget's row-to-screen navigation: which screen to open, and how
// its params map to fields of the clicked row.
type Open struct {
	Screen string            `json:"screen" yaml:"screen"`
	Param  map[string]string `json:"param" yaml:"param"` // screen param -> row field
}

// widgetSpec is the value half of a widget's single-key YAML map.
type widgetSpec struct {
	From    string   `yaml:"from"`
	Columns []string `yaml:"columns"`
	Keys    []string `yaml:"keys"`
	Open    *Open    `yaml:"open"`
}

// UnmarshalYAML reads a widget from its single-key map: the key names the
// kind, the value decodes as its spec.
func (w *Widget) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode || len(value.Content) != 2 {
		return fmt.Errorf("a widget must be a single-key map")
	}
	var spec widgetSpec
	if err := value.Content[1].Decode(&spec); err != nil {
		return fmt.Errorf("widget %q: %w", value.Content[0].Value, err)
	}
	w.Kind = value.Content[0].Value
	w.From = spec.From
	w.Columns = spec.Columns
	w.Keys = spec.Keys
	w.Open = spec.Open
	return nil
}

var widgetKinds = map[string]bool{
	"table": true, "fields": true, "badge": true, "list": true,
	"text": true, "bins": true, "document": true, "file": true,
}

var serverBindings = map[string]bool{"store_root": true, "files_root": true}

// LoadViews reads and validates every view file at paths, using load to
// resolve each run and check its vars against the pack it loads. It fails at
// the first problem, naming the file and the screen.
func LoadViews(paths []string, load Loader) ([]View, error) {
	views := make([]View, 0, len(paths))
	ids := make(map[string]bool, len(paths))
	for _, path := range paths {
		v, err := readView(path)
		if err != nil {
			return nil, err
		}
		if ids[v.ID] {
			return nil, fmt.Errorf("web: %s: view id %q is used more than once", path, v.ID)
		}
		ids[v.ID] = true
		if err := v.validate(path, load); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// readView decodes one view file strictly: an unknown key is an error.
func readView(path string) (View, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return View{}, fmt.Errorf("web: %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var v View
	if err := dec.Decode(&v); err != nil {
		return View{}, fmt.Errorf("web: %s: %w", path, err)
	}
	v.ID = strings.TrimSuffix(filepath.Base(path), ".ui.yaml")
	v.dir = filepath.Dir(path)
	v.normalizeSlices()
	return v, nil
}

// normalizeSlices replaces every nil slice in v, and everything it contains,
// with an empty slice. A YAML view file that omits a list leaves the decoded
// field nil, and several of these fields lack "omitempty", so encoding/json
// would otherwise serve the browser a JSON null where it expects an array.
func (v *View) normalizeSlices() {
	if v.Discloses == nil {
		v.Discloses = []string{}
	}
	if v.Screens == nil {
		v.Screens = []Screen{}
	}
	for i := range v.Screens {
		v.Screens[i].normalizeSlices()
	}
}

func (s *Screen) normalizeSlices() {
	if s.Params == nil {
		s.Params = []string{}
	}
	if s.Show == nil {
		s.Show = []Widget{}
	}
	for i := range s.Show {
		s.Show[i].normalizeSlices()
	}
	if s.Actions == nil {
		s.Actions = []Action{}
	}
	for i := range s.Actions {
		s.Actions[i].normalizeSlices()
	}
}

func (a *Action) normalizeSlices() {
	if a.Input == nil {
		a.Input = []Input{}
	}
}

func (w *Widget) normalizeSlices() {
	if w.Columns == nil {
		w.Columns = []string{}
	}
	if w.Keys == nil {
		w.Keys = []string{}
	}
}

// validate checks every screen of v against the rules LoadViews documents.
func (v View) validate(path string, load Loader) error {
	seen := make(map[string]bool, len(v.Screens))
	for _, s := range v.Screens {
		if seen[s.ID] {
			return fmt.Errorf("web: %s: screen %q: duplicate screen id", path, s.ID)
		}
		seen[s.ID] = true
	}

	params := make(map[string][]string, len(v.Screens))
	for _, s := range v.Screens {
		params[s.ID] = s.Params
	}

	for _, s := range v.Screens {
		if err := v.validateScreen(path, s, params, load); err != nil {
			return err
		}
	}
	return nil
}

func (v View) validateScreen(path string, s Screen, screenParams map[string][]string, load Loader) error {
	for i, w := range s.Show {
		if err := validateWidget(w, screenParams); err != nil {
			return fmt.Errorf("web: %s: screen %q: widget %d: %w", path, s.ID, i, err)
		}
	}

	if s.Run != "" {
		bp, err := load(filepath.Join(v.dir, s.Run))
		if err != nil {
			return fmt.Errorf("web: %s: screen %q: run %q: %w", path, s.ID, s.Run, err)
		}
		ctx := bindingContext{params: s.Params}
		if err := validateVars(s.Vars, bp, ctx); err != nil {
			return fmt.Errorf("web: %s: screen %q: %w", path, s.ID, err)
		}
	}

	for ai, a := range s.Actions {
		if a.Run == "" {
			continue
		}
		bp, err := load(filepath.Join(v.dir, a.Run))
		if err != nil {
			return fmt.Errorf("web: %s: screen %q: action %d: run %q: %w", path, s.ID, ai, a.Run, err)
		}
		names := make([]string, len(a.Input))
		for i, in := range a.Input {
			names[i] = in.Name
		}
		ctx := bindingContext{params: s.Params, inputs: names, inAction: true}
		if err := validateVars(a.Vars, bp, ctx); err != nil {
			return fmt.Errorf("web: %s: screen %q: action %d: %w", path, s.ID, ai, err)
		}
	}
	return nil
}

func validateWidget(w Widget, screenParams map[string][]string) error {
	if !widgetKinds[w.Kind] {
		return fmt.Errorf("unknown kind %q", w.Kind)
	}
	if w.Open == nil {
		return nil
	}
	target, ok := screenParams[w.Open.Screen]
	if !ok {
		return fmt.Errorf("open screen %q does not exist", w.Open.Screen)
	}
	for key := range w.Open.Param {
		if !slices.Contains(target, key) {
			return fmt.Errorf("open param %q is not a param of screen %q", key, w.Open.Screen)
		}
	}
	return nil
}

// validateVars checks that every key of vars is a var bp declares, and that
// every value's binding source is allowed for ctx.
func validateVars(vars map[string]string, bp domain.Blueprint, ctx bindingContext) error {
	for name, binding := range vars {
		if _, ok := bp.Vars[name]; !ok {
			return fmt.Errorf("var %q: pack %q has no such var", name, bp.Name)
		}
		if err := ctx.check(binding); err != nil {
			return fmt.Errorf("var %q: %w", name, err)
		}
	}
	return nil
}

// bindingContext is where a var's value may come from: a screen's declared
// params, and, inside an action, its declared inputs.
type bindingContext struct {
	params   []string
	inputs   []string
	inAction bool
}

func (c bindingContext) check(binding string) error {
	source, rest, ok := strings.Cut(binding, ".")
	if !ok {
		return fmt.Errorf("binding %q has no source", binding)
	}
	switch source {
	case "param":
		if !slices.Contains(c.params, rest) {
			return fmt.Errorf("param %q is not a param of this screen", rest)
		}
	case "input":
		if !c.inAction {
			return fmt.Errorf("input %q: a screen's own vars may not use input", rest)
		}
		if !slices.Contains(c.inputs, rest) {
			return fmt.Errorf("input %q is not one of this action's inputs", rest)
		}
	case "state":
		if !c.inAction {
			return fmt.Errorf("state %q is allowed only in actions", rest)
		}
	case "const":
		// any literal is fine
	case "server":
		if !serverBindings[rest] {
			return fmt.Errorf("server %q must be store_root or files_root", rest)
		}
	default:
		return fmt.Errorf("binding %q has an unknown source", binding)
	}
	return nil
}

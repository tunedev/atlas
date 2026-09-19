package app

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// selectKey is the runner's own instruction inside a step's config: it names
// the path to narrow the tool's result to before storing it. It is stripped
// before the tool is invoked, because narrowing a result is the runner's
// business and a tool that knew about it would be less generic.
const selectKey = "select"

// Runner executes a blueprint: resolve each step's tool, render its config
// against accumulated state, invoke, narrow, store.
//
// Nothing here knows what any tool does or what any pack is for.
type Runner struct {
	registry ports.Registry
	tracer   trace.Tracer
}

// NewRunner builds a Runner against the given registry. It traces with a
// no-op tracer unless a real one is set through WithTracer, so a caller that
// does not care about tracing pays nothing and writes nothing extra.
func NewRunner(r ports.Registry) *Runner {
	return &Runner{registry: r, tracer: noop.NewTracerProvider().Tracer("")}
}

// WithTracer replaces the Runner's tracer, returning the same Runner for
// chaining at construction time.
func (r *Runner) WithTracer(t trace.Tracer) *Runner {
	r.tracer = t
	return r
}

// Run executes every step in order and stops at the first failure. The
// returned State carries each completed step's output keyed by step id.
func (r *Runner) Run(ctx context.Context, b domain.Blueprint) (*domain.State, error) {
	ctx, span := r.tracer.Start(ctx, "blueprint."+b.Name)
	defer span.End()
	span.SetAttributes(attribute.Int("blueprint.steps", len(b.Steps)))

	state := domain.NewState(b.Vars)
	for _, s := range b.Steps {
		if err := r.runStep(ctx, b.Name, s, state); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}
	return state, nil
}

// runStep is where a span per tool invocation is opened. Instrumenting here
// rather than inside each tool is what stops a newly added tool arriving
// untraced, and traces are how a pack author -- who cannot read Go -- finds
// out which step was slow or wrong.
func (r *Runner) runStep(ctx context.Context, blueprint string, s domain.Step, state *domain.State) error {
	ctx, span := r.tracer.Start(ctx, "tool."+s.Tool)
	defer span.End()
	span.SetAttributes(
		attribute.String("blueprint.name", blueprint),
		attribute.String("step.id", s.ID),
		attribute.String("tool.name", s.Tool),
	)

	tool, ok := r.registry.Lookup(s.Tool)
	if !ok {
		err := fmt.Errorf("blueprint %s: step %s: no tool named %q", blueprint, s.ID, s.Tool)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	with, path, err := renderConfig(s, state)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	result, err := tool.Invoke(ctx, with)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	narrowed, err := Select(result, path)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("blueprint %s: step %s: %w", blueprint, s.ID, err)
	}

	state.Put(s.ID, narrowed)
	return nil
}

// renderConfig evaluates every config value as a template and lifts out the
// runner's own select key.
func renderConfig(s domain.Step, state *domain.State) (map[string]string, string, error) {
	with := make(map[string]string, len(s.With))
	var path string

	for key, raw := range s.With {
		rendered, err := Render(raw, state)
		if err != nil {
			return nil, "", err
		}
		if key == selectKey {
			path = rendered
			continue
		}
		with[key] = rendered
	}
	return with, path, nil
}

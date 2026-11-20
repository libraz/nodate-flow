// Package engine is the differential constraint evaluator. It takes a task's current facts plus the set of DSL
// expressions attached to it and writes satisfied/failed markers
// back through a Store interface.
//
// The evaluation loop owns no database coupling: it consumes a [Store]
// interface, a pure [constraint.Facts] value and an [Engine.Emit] callback,
// so it can be unit-tested with in-memory fakes. The database-backed entry
// points ([Evaluate], [EvaluateTaskTx], [ListDependents]) live in
// entry.go and are the only way production code runs an evaluation.
package engine

import (
	"context"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
)

// Row is one task_constraints row, as the engine needs to see it.
// Expression is the raw JSON bytes from task_constraints.expression.
type Row struct {
	PublicID   string
	Kind       string
	Expression []byte
}

// Task is what [Store.LoadTask] returns: the task's public id, its facts
// and its enabled constraint rows. Found is false when the task is
// disabled or absent from the workspace; nothing is evaluated then.
type Task struct {
	PublicID string
	Facts    constraint.Facts
	Rows     []Row
	Found    bool
}

// Store abstracts the persistence boundary so the engine can be
// exercised with an in-memory fake.
type Store interface {
	// LoadTask returns the task's facts plus every enabled
	// task_constraints row for the given internal task id.
	LoadTask(ctx context.Context, taskID uint32) (Task, error)
	// MarkSatisfied flips a constraint row to satisfied as of now and
	// reports whether the stored verdict changed.
	MarkSatisfied(ctx context.Context, publicID string, now time.Time) (changed bool, err error)
	// MarkFailed flips a constraint row to failing as of now and reports
	// whether the stored verdict changed.
	MarkFailed(ctx context.Context, publicID string, now time.Time) (changed bool, err error)
}

// EmitFunc records one changed verdict. It is called once per row whose
// stored verdict changed, after the marker was written.
type EmitFunc func(ctx context.Context, taskID uint32, taskPublicID string, row Row, satisfied bool) error

// Outcome is the per-row result surfaced to callers (metrics, tests,
// event payloads). It is intentionally small — the engine never
// exposes the raw AST.
type Outcome struct {
	PublicID   string
	Kind       string
	Expression string
	Satisfied  bool
	// Changed reports that this evaluation moved the stored verdict,
	// including the first verdict on a row that had none.
	Changed bool
	// ParseError is populated when the stored expression could not
	// be decoded. The row is left alone in that case: operator has
	// to fix the bad DSL before the engine will touch it again.
	ParseError error
}

// Engine evaluates constraints, writes the resulting markers through
// Store and reports every changed verdict through Emit. It is safe to
// construct per call; there is no mutable state.
type Engine struct {
	Store Store
	// Emit receives each changed verdict. A change with no Emit is an
	// error rather than a silently dropped event.
	Emit EmitFunc
	// Now lets tests freeze the clock. When nil, time.Now is used.
	Now func() time.Time
}

// errEmitMissing is returned when a verdict changed and no Emit was set.
var errEmitMissing = errors.New("engine: verdict changed but Emit is not set")

// evaluate loads a task's constraints, evaluates each against the task
// facts, writes the resulting marker for every row it evaluated and emits
// each changed verdict. The store keeps the time a row first reached its
// current outcome, so re-evaluating an unchanged row moves nothing and
// emits nothing. The returned slice mirrors the stored order.
func (e *Engine) evaluate(ctx context.Context, taskID uint32) ([]Outcome, error) {
	if e.Store == nil {
		return nil, errors.New("engine: Store is required")
	}
	task, err := e.Store.LoadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !task.Found {
		return nil, nil
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	outcomes := make([]Outcome, 0, len(task.Rows))
	for _, r := range task.Rows {
		out := Outcome{PublicID: r.PublicID, Kind: r.Kind, Expression: string(r.Expression)}
		parsed, perr := constraint.Parse(r.Expression)
		if perr != nil {
			out.ParseError = perr
			outcomes = append(outcomes, out)
			continue
		}
		ok, eerr := constraint.Evaluate(parsed, task.Facts)
		if eerr != nil {
			out.ParseError = eerr
			outcomes = append(outcomes, out)
			continue
		}
		out.Satisfied = ok
		var changed bool
		if ok {
			changed, err = e.Store.MarkSatisfied(ctx, r.PublicID, now)
		} else {
			changed, err = e.Store.MarkFailed(ctx, r.PublicID, now)
		}
		if err != nil {
			outcomes = append(outcomes, out)
			return outcomes, err
		}
		out.Changed = changed
		outcomes = append(outcomes, out)
		if !changed {
			continue
		}
		if e.Emit == nil {
			return outcomes, errEmitMissing
		}
		if err := e.Emit(ctx, taskID, task.PublicID, r, ok); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

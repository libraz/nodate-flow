// Package engine is the differential constraint evaluator. It takes a task's current facts plus the set of DSL
// expressions attached to it and writes satisfied/failed markers
// back through a Store interface.
//
// The engine deliberately owns no database coupling: it consumes a
// [Store] interface and a pure [constraint.Facts] value so it can be
// unit-tested with an in-memory fake and later wired to sqlc in a
// thin adapter. That keeps replay equivalence a property
// of a single, side-effect-free Go package instead of leaking DB
// quirks into the evaluation path.
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
	Expression []byte
}

// Store abstracts the persistence boundary so the engine can be
// exercised with an in-memory fake. The concrete adapter lives in
// the router / handler layer and wraps sqlc-generated queries.
type Store interface {
	// LoadTask returns the current facts plus every enabled
	// task_constraints row for the given internal task id.
	LoadTask(ctx context.Context, taskID uint32) (constraint.Facts, []Row, error)
	// MarkSatisfied flips a constraint row to satisfied as of now.
	MarkSatisfied(ctx context.Context, publicID string, now time.Time) error
	// MarkFailed flips a constraint row to failing as of now.
	MarkFailed(ctx context.Context, publicID string, now time.Time) error
}

// Outcome is the per-row result surfaced to callers (metrics, tests,
// event payloads). It is intentionally small — the engine never
// exposes the raw AST.
type Outcome struct {
	PublicID  string
	Satisfied bool
	// ParseError is populated when the stored expression could not
	// be decoded. The row is left alone in that case: operator has
	// to fix the bad DSL before the engine will touch it again.
	ParseError error
}

// Engine evaluates constraints and writes the resulting markers
// through Store. It is safe to construct per call; there is no
// mutable state.
type Engine struct {
	Store Store
	// Now lets tests freeze the clock. When nil, time.Now is used.
	Now func() time.Time
}

// EvaluateTask loads a task's constraints, evaluates each against the
// task facts, and writes the resulting satisfied/failed marker for
// every row it evaluated — the write is unconditional, so a row whose
// outcome is unchanged is written the same values again. The returned
// slice mirrors the stored order so callers can surface per-constraint
// feedback in the UI.
func (e *Engine) EvaluateTask(ctx context.Context, taskID uint32) ([]Outcome, error) {
	if e.Store == nil {
		return nil, errors.New("engine: Store is required")
	}
	facts, rows, err := e.Store.LoadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	outcomes := make([]Outcome, 0, len(rows))
	for _, r := range rows {
		parsed, perr := constraint.Parse(r.Expression)
		if perr != nil {
			outcomes = append(outcomes, Outcome{PublicID: r.PublicID, ParseError: perr})
			continue
		}
		ok, eerr := constraint.Evaluate(parsed, facts)
		if eerr != nil {
			outcomes = append(outcomes, Outcome{PublicID: r.PublicID, ParseError: eerr})
			continue
		}
		outcomes = append(outcomes, Outcome{PublicID: r.PublicID, Satisfied: ok})
		if ok {
			if err := e.Store.MarkSatisfied(ctx, r.PublicID, now); err != nil {
				return outcomes, err
			}
		} else {
			if err := e.Store.MarkFailed(ctx, r.PublicID, now); err != nil {
				return outcomes, err
			}
		}
	}
	return outcomes, nil
}

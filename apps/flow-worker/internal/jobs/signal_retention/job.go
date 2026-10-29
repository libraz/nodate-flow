// Package signal_retention implements the worker job that deletes signals
// past their expires_at which the Applier never applied (ADR 0008 D1/D8).
// Without it the signals table, and every inbox read windowed over it,
// grows without bound.
//
// Each tick deletes in fixed-size batches through the generated
// DeleteExpiredUnappliedSignals query until a batch deletes nothing or
// maxBatchesPerTick is reached; the remainder is picked up next tick, so
// one tick never holds the runner for an unbounded time.
package signal_retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

const (
	// JobName is the stable identifier the runner uses for log fields.
	// MUST NOT change once a deploy has observed it.
	JobName = "signal_retention"

	// maxBatchesPerTick bounds the statements one tick issues. Each
	// statement deletes at most the query's LIMIT.
	maxBatchesPerTick = 50
)

// Sweeper is the delete the job issues. *generated.Queries satisfies it.
type Sweeper interface {
	DeleteExpiredUnappliedSignals(ctx context.Context, expiresAt sql.NullTime) (int64, error)
}

// Job deletes expired, unapplied signals on every runner tick.
type Job struct {
	// Signals is the delete surface. Required.
	Signals Sweeper
	// Logger receives one record per tick that deleted rows or failed.
	// Required.
	Logger *slog.Logger
}

// New constructs a Job deleting through the supplied database pool.
func New(db *sql.DB, logger *slog.Logger) (*Job, error) {
	if db == nil {
		return nil, errors.New("signal_retention: db is required")
	}
	if logger == nil {
		return nil, errors.New("signal_retention: logger is required")
	}
	return &Job{Signals: generated.New(db), Logger: logger}, nil
}

// Name returns the stable runner identifier for the retention job.
func (j *Job) Name() string { return JobName }

// Tick deletes signals whose expires_at is before now, batch by batch.
func (j *Job) Tick(ctx context.Context, now time.Time) error {
	cutoff := sql.NullTime{Time: now.UTC(), Valid: true}
	var total int64
	for range maxBatchesPerTick {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := j.Signals.DeleteExpiredUnappliedSignals(ctx, cutoff)
		if err != nil {
			j.Logger.ErrorContext(ctx, "signal_retention: sweep failed",
				slog.Any("err", err), logutil.LogNumber("deleted", total))
			return fmt.Errorf("signal_retention: delete expired signals: %w", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	if total > 0 {
		j.Logger.InfoContext(ctx, "signal_retention: deleted expired signals", logutil.LogNumber("deleted", total))
	}
	return nil
}

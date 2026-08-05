package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// PurgeQuerier is the narrow contract [Purger] needs from the sqlc
// query bundle. It keeps this file free of the heavy generated import
// so unit tests can supply a fake without spinning up a DB.
type PurgeQuerier interface {
	PurgeFinishedAgentRuns(ctx context.Context, finishedAt sql.NullTime) error
	RequeueStrandedAgentRuns(ctx context.Context, arg generated.RequeueStrandedAgentRunsParams) (int64, error)
	FailExhaustedAgentRuns(ctx context.Context, arg generated.FailExhaustedAgentRunsParams) (int64, error)
}

// Defaults for the stranded-run reaper.
const (
	// DefaultStrandedAfter is how long a run may stay 'claimed' before
	// the worker holding it is presumed gone.
	//
	// It is generous because a legitimate run is an LLM call and can
	// take minutes; requeueing one that is still executing would spend
	// a second model call on work already in progress. Thirty minutes
	// is far beyond any single tick and still far below the point where
	// a stuck agent stops mattering to the user.
	DefaultStrandedAfter = 30 * time.Minute
	// DefaultMaxAttempts is how many times a run may be claimed before
	// it is failed instead of requeued.
	//
	// A run that strands repeatedly is most likely killing its worker,
	// and every attempt costs a model call, so the budget is small.
	// Webhook deliveries are retried far more freely because a retry
	// there is one HTTP request the receiver already knows how to
	// deduplicate.
	DefaultMaxAttempts = 3
)

// strandedReason is written to agent_runs.error_message when a run is
// failed for stranding too often.
const strandedReason = "agent run was claimed but never finished; the worker holding it stopped, and the retry budget is now exhausted"

// Purger periodically deletes succeeded / failed rows from agent_runs
// so the queue / history table does not grow unbounded. It ticks on
// its own [time.Ticker]; run one per api replica or run a single
// dedicated "maintenance" replica — the DELETE is idempotent.
type Purger struct {
	Queries   PurgeQuerier
	Interval  time.Duration
	Retention time.Duration
	Logger    *slog.Logger
	Now       func() time.Time

	// StrandedAfter and MaxAttempts override [DefaultStrandedAfter] and
	// [DefaultMaxAttempts]. Zero means the default.
	StrandedAfter time.Duration
	MaxAttempts   uint8

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

// ErrPurgerAlreadyRunning is returned by [Purger.Start] when the
// purger has already been started.
var ErrPurgerAlreadyRunning = errors.New("agentruntime: purger already running")

// Start begins the purge loop in a background goroutine. Defaults:
// 1h interval, 7d retention, [slog.Default] logger, [time.Now] clock.
func (p *Purger) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return ErrPurgerAlreadyRunning
	}
	if p.Interval <= 0 {
		p.Interval = time.Hour
	}
	if p.Retention <= 0 {
		p.Retention = 7 * 24 * time.Hour
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.running = true
	p.mu.Unlock()

	go bgloop.Run(runCtx, "agent_runs.purger", p.Logger, p.loop)
	return nil
}

// Stop signals the loop to exit. Safe for concurrent and repeat calls.
func (p *Purger) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.running = false
}

func (p *Purger) loop(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	// Run one pass immediately so slow cold boots still get a
	// cleanup within the first tick instead of waiting a full hour.
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

func (p *Purger) tick(ctx context.Context) {
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	p.reapStranded(ctx)

	cutoff := p.Now().UTC().Add(-p.Retention)
	err := p.Queries.PurgeFinishedAgentRuns(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		p.Logger.Warn("agent_runs purge failed", "err", err, "cutoff", cutoff)
		return
	}
	p.Logger.Debug("agent_runs purged", "cutoff", cutoff)
}

// reapStranded rescues runs abandoned in 'claimed'.
//
// A worker that dies between claiming a run and finishing it leaves the
// row claimed forever. Nothing scans that status, so the run never
// executes — and because the scheduler's dedupe key is still held by
// the row, the same agent is never enqueued again either. One killed
// worker silently retires the agents it happened to be holding.
//
// Runs with budget left go back to pending; the rest are failed with a
// reason, because an unbounded retry of a run that keeps killing its
// worker costs a model call every round.
func (p *Purger) reapStranded(ctx context.Context) {
	cutoff := p.Now().UTC().Add(-p.strandedAfter())
	maxAttempts := p.maxAttempts()

	failed, err := p.Queries.FailExhaustedAgentRuns(ctx, generated.FailExhaustedAgentRunsParams{
		ErrorMessage: sql.NullString{String: strandedReason, Valid: true},
		ClaimedAt:    sql.NullTime{Time: cutoff, Valid: true},
		Attempts:     maxAttempts,
	})
	if err != nil {
		p.Logger.Warn("agent_runs stranded reap failed", "err", err, "cutoff", cutoff)
	} else if failed > 0 {
		p.Logger.Warn("agent runs failed after stranding past their retry budget",
			"count", failed, "cutoff", cutoff, "max_attempts", maxAttempts)
	}

	requeued, err := p.Queries.RequeueStrandedAgentRuns(ctx, generated.RequeueStrandedAgentRunsParams{
		ClaimedAt: sql.NullTime{Time: cutoff, Valid: true},
		Attempts:  maxAttempts,
	})
	if err != nil {
		p.Logger.Warn("agent_runs stranded requeue failed", "err", err, "cutoff", cutoff)
		return
	}
	if requeued > 0 {
		p.Logger.Warn("agent runs requeued after their worker stopped holding them",
			"count", requeued, "cutoff", cutoff)
	}
}

func (p *Purger) strandedAfter() time.Duration {
	if p.StrandedAfter > 0 {
		return p.StrandedAfter
	}
	return DefaultStrandedAfter
}

func (p *Purger) maxAttempts() uint8 {
	if p.MaxAttempts > 0 {
		return p.MaxAttempts
	}
	return DefaultMaxAttempts
}

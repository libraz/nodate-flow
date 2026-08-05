package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

// PurgeQuerier is the narrow contract [Purger] needs from the sqlc
// query bundle. It keeps this file free of the heavy generated import
// so unit tests can supply a fake without spinning up a DB.
type PurgeQuerier interface {
	PurgeFinishedAgentRuns(ctx context.Context, finishedAt sql.NullTime) error
}

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
	cutoff := p.Now().UTC().Add(-p.Retention)
	err := p.Queries.PurgeFinishedAgentRuns(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		p.Logger.Warn("agent_runs purge failed", "err", err, "cutoff", cutoff)
		return
	}
	p.Logger.Debug("agent_runs purged", "cutoff", cutoff)
}

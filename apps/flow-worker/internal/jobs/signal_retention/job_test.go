package signal_retention

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// stubSweeper returns the queued batch sizes in order, then 0, and
// records the cutoff it was asked for.
type stubSweeper struct {
	batches []int64
	err     error
	calls   int
	cutoff  sql.NullTime
}

func (s *stubSweeper) DeleteExpiredUnappliedSignals(_ context.Context, expiresAt sql.NullTime) (int64, error) {
	s.calls++
	s.cutoff = expiresAt
	if s.err != nil {
		return 0, s.err
	}
	if len(s.batches) == 0 {
		return 0, nil
	}
	n := s.batches[0]
	s.batches = s.batches[1:]
	return n, nil
}

func newJob(s *stubSweeper) *Job {
	return &Job{Signals: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestTickRepeatsUntilABatchDeletesNothing(t *testing.T) {
	t.Parallel()
	s := &stubSweeper{batches: []int64{1000, 1000, 3}}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if err := newJob(s).Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if s.calls != 4 {
		t.Fatalf("calls = %d, want 4 (three batches, then an empty one)", s.calls)
	}
	if !s.cutoff.Valid || !s.cutoff.Time.Equal(now) {
		t.Fatalf("cutoff = %v, want the tick instant %v", s.cutoff, now)
	}
}

func TestTickStopsAtTheBatchCeiling(t *testing.T) {
	t.Parallel()
	batches := make([]int64, maxBatchesPerTick+10)
	for i := range batches {
		batches[i] = 1000
	}
	s := &stubSweeper{batches: batches}
	if err := newJob(s).Tick(context.Background(), time.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if s.calls != maxBatchesPerTick {
		t.Fatalf("calls = %d, want the ceiling %d", s.calls, maxBatchesPerTick)
	}
}

func TestTickReturnsTheDeleteError(t *testing.T) {
	t.Parallel()
	boom := errors.New("lock wait timeout")
	s := &stubSweeper{err: boom}
	if err := newJob(s).Tick(context.Background(), time.Now()); !errors.Is(err, boom) {
		t.Fatalf("Tick err = %v, want %v", err, boom)
	}
}

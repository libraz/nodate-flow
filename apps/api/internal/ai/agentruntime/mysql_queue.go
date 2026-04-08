package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// MySQLQueue is a [Queue] backed by the agent_runs table. It supports
// multi-replica scheduler / worker deployments via the UNIQUE
// dedupe_key column (prevents double enqueue across schedulers) and
// SELECT ... FOR UPDATE SKIP LOCKED (lets N workers race for rows
// without contention).
//
// Claim uses a short-poll loop because MySQL has no server-push
// primitive like Redis BLPOP or Postgres LISTEN. PollInterval controls
// how often idle workers wake up to check for new rows.
type MySQLQueue struct {
	DB           *sql.DB
	PollInterval time.Duration
}

// NewMySQLQueue constructs a MySQLQueue with a default poll interval
// of 1 second. Operators can tune the interval via the PollInterval
// field after construction.
func NewMySQLQueue(db *sql.DB) *MySQLQueue {
	return &MySQLQueue{DB: db, PollInterval: time.Second}
}

// Enqueue inserts a new agent_runs row. Duplicate dedupe_key
// violations are translated to [ErrDuplicate] so the scheduler can
// treat them as no-ops across replicas.
func (q *MySQLQueue) Enqueue(ctx context.Context, r Run) error {
	const ins = `INSERT INTO agent_runs
		(public_id, workspace_id, agent_id, dedupe_key, scheduled_at)
		VALUES (?, ?, ?, ?, ?)`
	pub := types.New()
	_, err := q.DB.ExecContext(ctx, ins,
		pub, r.Job.WsID, r.Job.AgentID, r.DedupeKey, r.ScheduledAt.UTC(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

// Claim polls agent_runs for a pending row, marks it claimed in the
// same transaction, and returns it. Returns ctx.Err() on cancel.
func (q *MySQLQueue) Claim(ctx context.Context) (Run, error) {
	interval := q.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	for {
		r, ok, err := q.tryClaim(ctx)
		if err != nil {
			return Run{}, err
		}
		if ok {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return Run{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (q *MySQLQueue) tryClaim(ctx context.Context) (Run, bool, error) {
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	const sel = `SELECT id, workspace_id, agent_id, dedupe_key, scheduled_at, attempts
		FROM agent_runs
		WHERE status = 'pending' AND enabled = TRUE
		ORDER BY scheduled_at ASC, id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`
	var (
		id          uint32
		wsID        uint32
		agentID     uint32
		dedupeKey   string
		scheduledAt time.Time
		attempts    int
	)
	row := tx.QueryRowContext(ctx, sel)
	if err := row.Scan(&id, &wsID, &agentID, &dedupeKey, &scheduledAt, &attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	const upd = `UPDATE agent_runs
		SET status = 'claimed', claimed_at = ?, attempts = attempts + 1
		WHERE id = ?`
	if _, err := tx.ExecContext(ctx, upd, time.Now().UTC(), id); err != nil {
		return Run{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return Run{
		DedupeKey:   dedupeKey,
		Job:         Job{AgentID: agentID, WsID: wsID},
		ScheduledAt: scheduledAt,
		Attempts:    attempts + 1,
	}, true, nil
}

// Ack marks the run as succeeded.
func (q *MySQLQueue) Ack(ctx context.Context, dedupeKey string) error {
	const upd = `UPDATE agent_runs
		SET status = 'succeeded', finished_at = ?
		WHERE dedupe_key = ? AND status = 'claimed'`
	_, err := q.DB.ExecContext(ctx, upd, time.Now().UTC(), dedupeKey)
	return err
}

// Nack parks the row with the failure message. Retry policy is left
// to the caller — set status back to 'pending' explicitly if a retry
// is desired.
func (q *MySQLQueue) Nack(ctx context.Context, dedupeKey string, runErr error) error {
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	const upd = `UPDATE agent_runs
		SET status = 'failed', finished_at = ?, error_message = ?
		WHERE dedupe_key = ? AND status = 'claimed'`
	_, err := q.DB.ExecContext(ctx, upd, time.Now().UTC(), sql.NullString{String: msg, Valid: msg != ""}, dedupeKey)
	return err
}

// isDuplicateKey detects MySQL error 1062 without taking a hard
// dependency on the mysql driver package. Matching on the substring
// keeps this package driver-agnostic and easy to unit-test.
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Error 1062") || strings.Contains(s, "Duplicate entry")
}

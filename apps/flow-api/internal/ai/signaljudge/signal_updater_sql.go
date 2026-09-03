// Package signaljudge — SQL-backed adapters for SignalUpdater and
// TaskResolver. These bind the Applier interfaces to the production
// database without going through the full sqlc surface for a single
// UPDATE / SELECT — the queries are small enough that raw SQL stays
// reviewable, and tests inject fakes against the same interfaces so
// the sqlc surface does not grow for a single wiring concern.
package signaljudge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// SQLSignalUpdater writes signals.judge_run_id / judge_output_json /
// confidence / applied_at via raw SQL. Implements [SignalUpdater].
//
// The UPDATE is deliberately narrow — it touches only the four
// judge-output columns and leaves the surrounding row alone so a
// concurrent enabled/disabled toggle from the user-facing UI cannot
// race with the Applier's write.
type SQLSignalUpdater struct {
	DB *sql.DB
}

// UpdateJudgeOutput implements [SignalUpdater].
func (u *SQLSignalUpdater) UpdateJudgeOutput(ctx context.Context, signalInternalID int64, runID uint32, output json.RawMessage, confidence float64, appliedAt *time.Time) error {
	if u == nil || u.DB == nil {
		return nil
	}
	// confidence is stored as DECIMAL(3,2); FormatFloat produces a
	// stable decimal representation MySQL casts losslessly into the
	// column. We clamp to [0.0, 1.0] defensively even though
	// ValidateVerdict already rejected out-of-range values.
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	confStr := strconv.FormatFloat(confidence, 'f', 2, 64)
	runIDArg := sql.NullInt32{}
	if runID > 0 {
		runIDArg = sql.NullInt32{Int32: int32(runID), Valid: true} //#nosec G115 -- agent_runs.id is INT UNSIGNED, fits int32 within realistic deployments
	}
	appliedArg := sql.NullTime{}
	if appliedAt != nil {
		appliedArg = sql.NullTime{Time: appliedAt.UTC(), Valid: true}
	}
	const q = `UPDATE signals
		SET judge_run_id = ?,
			judge_output_json = ?,
			confidence = ?,
			applied_at = ?
		WHERE id = ?`
	if _, err := u.DB.ExecContext(ctx, q, runIDArg, []byte(output), confStr, appliedArg, signalInternalID); err != nil {
		return fmt.Errorf("signaljudge: update signals: %w", err)
	}
	return nil
}

// SQLTaskResolver looks up a task's internal id by public id inside
// a workspace. Implements [TaskResolver].
type SQLTaskResolver struct {
	DB *sql.DB
}

// ResolveTask implements [TaskResolver]. Returns (0, false, nil) when
// the public id does not parse or the task does not exist in the
// workspace, mirroring the existing signal-handler resolver shape so
// downstream code can branch on `ok` rather than err-or-zero.
func (r *SQLTaskResolver) ResolveTask(ctx context.Context, workspaceID uint32, publicID string) (int64, bool, error) {
	if r == nil || r.DB == nil {
		return 0, false, nil
	}
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, false, nil
	}
	const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id int64
	if err := r.DB.QueryRowContext(ctx, q, workspaceID, pub).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

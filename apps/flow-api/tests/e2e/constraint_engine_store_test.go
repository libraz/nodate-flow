package e2e

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// errEngineStoreDown is what the injected outage reports.
var errEngineStoreDown = errors.New("e2e: engine store unreachable")

// alwaysFailingConnector yields a *sql.DB that never produces a
// connection, so every statement routed to it fails.
type alwaysFailingConnector struct{}

func (alwaysFailingConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errEngineStoreDown
}

func (alwaysFailingConnector) Driver() driver.Driver { return alwaysFailingDriver{} }

type alwaysFailingDriver struct{}

func (alwaysFailingDriver) Open(string) (driver.Conn, error) { return nil, errEngineStoreDown }

// The constraint engine writes its verdicts back to task_constraints,
// and those markers are what the task panel reads as "this deadline is
// met". A fact the engine could not read is not a fact: if the due_on
// lookup fails and the failure is absorbed, the task looks like it has
// no deadline, every time.due_* builtin answers false for it, and the
// engine records a definitive verdict about a date nobody managed to
// look at.
//
// The failure is injected at the single statement it belongs to rather
// than by breaking the whole connection, because breaking everything
// makes any later read fail too and the test would pass whether the
// due_on error is handled or swallowed.

// failingDueOnDBTX routes the engine's due_on read to a closed database
// and everything else to the real one.
type failingDueOnDBTX struct {
	real   *sql.DB
	broken *sql.DB
}

func (d failingDueOnDBTX) pick(query string) *sql.DB {
	if strings.Contains(query, "GetTaskDueOnForEngine") {
		return d.broken
	}
	return d.real
}

func (d failingDueOnDBTX) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	return d.pick(q).ExecContext(ctx, q, args...)
}

func (d failingDueOnDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return d.pick(q).PrepareContext(ctx, q)
}

func (d failingDueOnDBTX) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	return d.pick(q).QueryContext(ctx, q, args...)
}

func (d failingDueOnDBTX) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	return d.pick(q).QueryRowContext(ctx, q, args...)
}

// TestConstraintEngineRecordsNoVerdictWhenTheDueDateIsUnreadable seeds a
// deadline constraint that is genuinely satisfied, then re-evaluates it
// with the due_on read broken. The engine must report the failure and
// leave the stored verdict alone.
func TestConstraintEngineRecordsNoVerdictWhenTheDueDateIsUnreadable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "engine: unreadable due date",
		"dueOn":     "2026-01-01",
	}, &task)
	require.NotEmpty(t, task.ID)

	var constraintRow struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2100-01-01"}`,
		}, &constraintRow)
	require.NotEmpty(t, constraintRow.ID)

	// Adding a constraint evaluates it, and this one is met, so the row
	// starts out marked satisfied.
	satisfiedAt, failedAt := readConstraintMarkers(t, constraintRow.ID)
	require.True(t, satisfiedAt.Valid, "fixture: the constraint must start out satisfied")
	require.False(t, failedAt.Valid, "fixture: the constraint must not start out failing")

	// A database that answers nothing, standing in for the outage.
	broken := sql.OpenDB(alwaysFailingConnector{})
	require.NoError(t, broken.Close())

	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, task.ID)
	eng := &engine.Engine{Store: &engine.SqlcStore{
		WorkspaceID: wsID,
		Queries:     generated.New(failingDueOnDBTX{real: testDB, broken: broken}),
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	outcomes, err := eng.EvaluateTask(ctx, taskInternalID)
	require.Error(t, err,
		"an unreadable due date must be reported, not answered as 'no due date'")
	require.Empty(t, outcomes, "no constraint may be given a verdict from facts that failed to load")

	satisfiedAt, failedAt = readConstraintMarkers(t, constraintRow.ID)
	require.False(t, failedAt.Valid,
		"a constraint must not be recorded as failing because its due date could not be read")
	require.True(t, satisfiedAt.Valid,
		"the verdict from the last successful evaluation must survive an unreadable one")
}

// readConstraintMarkers returns the persisted verdict columns for a
// constraint public id.
func readConstraintMarkers(t *testing.T, publicID string) (satisfiedAt, failedAt sql.NullTime) {
	t.Helper()
	require.NoError(t, testDB.QueryRow(
		`SELECT satisfied_at, failed_at FROM task_constraints WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		publicID,
	).Scan(&satisfiedAt, &failedAt))
	return satisfiedAt, failedAt
}

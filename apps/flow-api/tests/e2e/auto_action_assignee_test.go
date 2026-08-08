package e2e

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
)

// backdateTask ages a task past the auto-action idle threshold so the
// rules that key off idleness are evaluated at all.
func backdateTask(t *testing.T, taskPublicID string, age time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-age)
	_, err := testDB.ExecContext(context.Background(), `
		UPDATE tasks SET updated_at = ?, created_at = ?
		WHERE public_id = UUID_TO_BIN(?, 0)`,
		at, at, taskPublicID)
	require.NoError(t, err)
}

// TestAutoActionExecutorSeesExistingAssignee is the regression for the
// has_assignee signal the executor computes for itself.
//
// Its query asked for task_actors.kind = 'assignee'. kind is the actor
// type — 'user' or 'agent' — and the relationship lives in role, so the
// predicate matched nothing the enum can hold and every task looked
// unassigned. Two rules read that one column, and both were wrong in
// opposite directions: assign_owner proposed an owner for tasks that
// already had one, and nudge_assignee, which requires an assignee,
// could never fire at all.
//
// The assertion is on the proposal rather than the column because the
// proposal is what reaches a person. A task created through the API
// gets its creator as assignee, so the task here has one.
func TestAutoActionExecutorSeesExistingAssignee(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	lockAutoActionPass(t)

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Idle, but owned"}, &task)
	require.NotEmpty(t, task.ID)

	var assignees int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM task_actors ta
		JOIN tasks t ON t.id = ta.task_id
		WHERE t.public_id = UUID_TO_BIN(?, 0) AND ta.role = 'assignee' AND ta.enabled = TRUE`,
		task.ID).Scan(&assignees))
	require.Positive(t, assignees, "the fixture depends on the task having an assignee")

	backdateTask(t, task.ID, 72*time.Hour)

	// The per-workspace confidence threshold defaults to 0.80, above
	// both rules under test (assign_owner 0.75, nudge_assignee 0.70), so
	// without this row the pass evaluates the task and discards whatever
	// it decided. Scoped to this tenant, so no other test's evaluation
	// changes.
	_, err := testDB.ExecContext(context.Background(), `
		INSERT INTO ai_settings (workspace_id, auto_action_threshold)
		VALUES ((SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)), 0.50)
		ON DUPLICATE KEY UPDATE auto_action_threshold = 0.50`, tt.WorkspacePublicID)
	require.NoError(t, err)

	// DryRun, and the decision is read from the executor's own log.
	//
	// A real pass is instance-wide: it evaluates every workspace and
	// would escalate, close or archive tasks belonging to whatever else
	// is running. Reading the log keeps the observation to this task and
	// leaves no rows behind for anyone else to trip over.
	logs := &captureWriter{}
	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			Interval:            time.Minute,
			ConfidenceThreshold: 0.5,
			DryRun:              true,
		},
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	require.NoError(t, exec.RunOnce(context.Background()))

	// has_assignee is not observable directly, so it is read through the
	// two rules that consume it: an owned task must not be proposed for
	// an owner, and an idle owned task is exactly what nudge is for.
	decided := logs.linesContaining(task.ID)
	require.NotEmpty(t, decided, "the pass must have evaluated this task")
	require.NotContains(t, decided, "assign_owner",
		"a task that already has an assignee must not be proposed for one")
	require.Contains(t, decided, "nudge_assignee",
		"an idle task with an assignee is what the nudge rule exists for")
}

// captureWriter collects log output so a test can read what the
// executor decided.
type captureWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// linesContaining returns the captured lines mentioning needle, joined
// so the caller can assert on their content.
func (w *captureWriter) linesContaining(needle string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, line := range strings.Split(w.buf.String(), "\n") {
		if strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

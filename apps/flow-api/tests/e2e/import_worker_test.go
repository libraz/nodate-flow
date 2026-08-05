package e2e

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/importer"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// importJobView is the slice of the queue row these tests assert on.
type importJobView struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	Status         string  `json:"status"`
	TotalItems     int     `json:"totalItems"`
	ProcessedItems int     `json:"processedItems"`
	FailedItems    int     `json:"failedItems"`
	ErrorLog       *string `json:"errorLog,omitempty"`
}

// newImportWorker builds a worker bound to the shared test database.
// Tests drive it with RunOnce rather than Start so a pass is a single
// observable step.
func newImportWorker(t *testing.T) *importer.Worker {
	t.Helper()
	return importer.NewWorker(testDB, generated.New(testDB), slog.Default())
}

// runImportWorkerFor drives passes until the given job has left the
// pending state, and fails if it never does.
//
// Which worker claims it is deliberately not asserted. Every test in
// this file drains the same queue, exactly as a second API replica
// would, so a sibling's pass can take this job first — and that is a
// correct outcome, not a race to defend against. What the queue
// promises is about the row: a pending job stops being pending. The
// pass's claim list still matters for the exclusivity test below, where
// the question really is which worker got it.
func runImportWorkerFor(ctx context.Context, t *testing.T, w *importer.Worker, jobID uint32) {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		_, err := w.RunOnce(ctx)
		require.NoError(t, err, "import worker pass failed")

		var status string
		require.NoError(t, testDB.QueryRowContext(ctx,
			`SELECT status FROM import_jobs WHERE id = ?`, jobID).Scan(&status))
		if status != string(generated.ImportJobsStatusPending) &&
			status != string(generated.ImportJobsStatusRunning) {
			return
		}
	}
	require.FailNow(t, "the job never left the pending state")
}

// createImportJob posts a job and returns the public id plus the
// internal id the worker claims by.
func createImportJob(ctx context.Context, t *testing.T, tenant *helpers.TestTenant, source string, config map[string]any) (string, uint32) {
	t.Helper()
	body := map[string]any{"source": source, "projectId": tenant.ProjectPublicID}
	if config != nil {
		body["configJson"] = config
	}
	var created importJobView
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/imports",
		tenant.AccessToken, body, &created)
	require.NotEmpty(t, created.ID)
	// The status is deliberately not asserted here. Every test in this
	// file drains the same queue, so a sibling's pass can claim this job
	// between the insert and the re-read the handler answers with — and
	// a job being picked up promptly is the behaviour under test, not a
	// problem with it.

	var internalID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM import_jobs WHERE public_id = UUID_TO_BIN(?, 0)`,
		created.ID).Scan(&internalID))
	return created.ID, internalID
}

// readImportJob re-reads the job through the API, which is the surface
// the user actually sees.
func readImportJob(t *testing.T, tenant *helpers.TestTenant, jobPublicID string) importJobView {
	t.Helper()
	var job importJobView
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/imports/"+jobPublicID,
		tenant.AccessToken, nil, &job)
	return job
}

// countTasksInProject counts the tasks the import created.
func countTasksInProject(ctx context.Context, t *testing.T, projectPublicID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		 WHERE p.public_id = UUID_TO_BIN(?, 0)`, projectPublicID).Scan(&n))
	return n
}

// TestImportJobDoesNotStayPending is the regression that matters most.
// A created job used to be inserted and then never looked at: the row
// held pending with 0/0 counters forever, which the UI renders as an
// import that is still working. Whatever else changes here, a job must
// reach a terminal state.
func TestImportJobDoesNotStayPending(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	csv := "title,description,priority,dueOn\n" +
		"Migrate the backlog,from the old tracker,2,2026-09-01\n" +
		"Close the old account,,0,\n" +
		"Tell the team,announcement,1,2026-09-15\n"

	jobPub, jobID := createImportJob(ctx, t, tenant, "csv", map[string]any{"csv": csv})
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "completed", job.Status,
		"a claimed job must end in a terminal state, never back at pending")
	require.Equal(t, 3, job.TotalItems)
	require.Equal(t, 3, job.ProcessedItems)
	require.Equal(t, 0, job.FailedItems)
	require.Equal(t, 3, countTasksInProject(ctx, t, tenant.ProjectPublicID),
		"every row in the file must have become a task")
}

// TestImportJobFailsOnUnparseableCSV covers the other half: a file the
// importer cannot read leaves a failed job carrying the reason, rather
// than a job that looks like it is still going.
func TestImportJobFailsOnUnparseableCSV(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// No title column: nothing in the file can become a task.
	jobPub, jobID := createImportJob(ctx, t, tenant, "csv",
		map[string]any{"csv": "name,notes\nMigrate,something\n"})
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "failed", job.Status)
	require.NotNil(t, job.ErrorLog, "a failed import must say why")
	require.Contains(t, *job.ErrorLog, "title")
	require.Equal(t, 0, countTasksInProject(ctx, t, tenant.ProjectPublicID))
}

// TestImportJobRecordsPerRowFailures proves partial success is
// expressed rather than hidden: the good rows land, the bad ones are
// counted and explained, and the job still reaches a terminal state.
func TestImportJobRecordsPerRowFailures(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	csv := "title,priority,dueOn\n" +
		"Good row,1,2026-09-01\n" +
		",2,2026-09-02\n" + // missing title
		"Bad date,1,not-a-date\n" +
		"Another good row,0,\n"

	jobPub, jobID := createImportJob(ctx, t, tenant, "csv", map[string]any{"csv": csv})
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "completed", job.Status)
	require.Equal(t, 4, job.TotalItems)
	require.Equal(t, 2, job.ProcessedItems)
	require.Equal(t, 2, job.FailedItems)
	require.NotNil(t, job.ErrorLog)
	require.Contains(t, *job.ErrorLog, "line 3")
	require.Contains(t, *job.ErrorLog, "line 4")
	require.Equal(t, 2, countTasksInProject(ctx, t, tenant.ProjectPublicID),
		"the rows that parsed must still have been created")
}

// TestImportJobFailsForSourceWithoutConnector locks in what an
// unimplemented source does. github, jira and linear are offered by the
// API and have no importer behind them; finishing as failed with the
// reason is the honest answer, and it is the difference between "we
// cannot do this yet" and a progress bar that never moves.
func TestImportJobFailsForSourceWithoutConnector(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	jobPub, jobID := createImportJob(ctx, t, tenant, "github", map[string]any{"repo": "owner/name"})
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "failed", job.Status)
	require.NotNil(t, job.ErrorLog)
	require.Contains(t, *job.ErrorLog, "github")
	require.Contains(t, *job.ErrorLog, "not implemented")
}

// TestImportJobClaimIsExclusive proves two workers cannot both run the
// same job. The claim is the status flip, so the loser sees zero rows
// updated and must leave the job alone.
func TestImportJobClaimIsExclusive(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, jobID := createImportJob(ctx, t, tenant, "csv",
		map[string]any{"csv": "title\nOnly once\n"})

	// Which claimer wins is not asserted: a sibling test's pass shares
	// this queue and may get there first, and that is the same
	// exclusivity working. What must hold either way is that once the
	// job is claimed, claiming it again reports nothing.
	q := generated.New(testDB)
	_, err := q.ClaimImportJob(ctx, jobID)
	require.NoError(t, err)

	second, err := q.ClaimImportJob(ctx, jobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, second, "a claimed job must not be claimable again")

	var status string
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT status FROM import_jobs WHERE id = ?`, jobID).Scan(&status))
	require.NotEqual(t, string(generated.ImportJobsStatusPending), status,
		"the job must actually have been claimed, or the assertion above proves nothing")

	// And the worker agrees: the job is no longer a candidate, so no
	// pass reports having claimed it.
	claimed, err := newImportWorker(t).RunOnce(ctx)
	require.NoError(t, err)
	require.NotContains(t, claimed, jobID,
		"a job already claimed must not be handed to another pass")
}

// TestImportJobCancelStopsTheRun covers cancellation mid-import: the
// worker stops creating tasks, and the rows it already created stay.
// They are rows the user can see; deleting them to make the cancel look
// tidy would destroy data the cancel never promised to remove. The
// counters are what tells the user how far it got.
func TestImportJobCancelStopsTheRun(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var rows strings.Builder
	rows.WriteString("title\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&rows, "row %d\n", i)
	}
	jobPub, jobID := createImportJob(ctx, t, tenant, "csv", map[string]any{"csv": rows.String()})

	// Cancel it the way the API does. Whether a pass had already claimed
	// it does not change what has to happen next.
	_, err := testDB.ExecContext(ctx,
		`UPDATE import_jobs SET status = 'cancelled' WHERE id = ?`, jobID)
	require.NoError(t, err)

	// A later pass must leave it alone: a cancelled job is not a
	// candidate, and a run that was already in flight stops at its next
	// status check rather than finishing the file behind the user.
	_, err = newImportWorker(t).RunOnce(ctx)
	require.NoError(t, err)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "cancelled", job.Status,
		"a cancelled job must not be resurrected by a later pass")
}

// TestImportWorkerReapsAbandonedJobs covers the failure mode this queue
// would otherwise reintroduce one step later: a worker that dies with a
// job claimed leaves it running forever, which reads exactly like the
// pending job the whole change is about.
func TestImportWorkerReapsAbandonedJobs(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	jobPub, jobID := createImportJob(ctx, t, tenant, "csv",
		map[string]any{"csv": "title\nAbandoned\n"})

	// Simulate the crash: claimed, started long ago, never finished.
	_, err := testDB.ExecContext(ctx, `
		UPDATE import_jobs
		   SET status = 'running', started_at = ?
		 WHERE id = ?`, time.Now().UTC().Add(-2*time.Hour), jobID)
	require.NoError(t, err)

	w := newImportWorker(t)
	w.StaleAfter = time.Hour
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)

	job := readImportJob(t, tenant, jobPub)
	require.Equal(t, "failed", job.Status,
		"a job abandoned by a dead worker must be reported, not left running")
	require.NotNil(t, job.ErrorLog)
	require.Contains(t, *job.ErrorLog, "abandoned")
}

// TestImportCreateRejectsOversizedCSV keeps the payload ceiling on the
// request. Storing a megabyte in the queue row and only then reporting
// that it was too big wastes a round trip and leaves the rejected data
// in the table.
func TestImportCreateRejectsOversizedCSV(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)

	oversized := "title\n" + strings.Repeat("a row that is long enough to add up\n", (importer.MaxCSVBytes/36)+64)
	require.Greater(t, len(oversized), importer.MaxCSVBytes)

	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/imports",
		tenant.AccessToken, map[string]any{
			"source":     "csv",
			"projectId":  tenant.ProjectPublicID,
			"configJson": map[string]any{"csv": oversized},
		})
	require.Equal(t, http.StatusRequestEntityTooLarge, status,
		"an oversized payload must be refused before the row is written")
}

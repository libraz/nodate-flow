// Package importer runs the queued import jobs that
// POST /workspaces/{wsId}/imports enqueues.
//
// The queue row is the whole contract with the user: it carries the
// lifecycle status the UI polls, the counters it renders as progress,
// and the error log it shows when something went wrong. A job that is
// enqueued and never picked up leaves all three frozen at their initial
// values, which reads as "still working" forever — so every path
// through this package ends with the row in a terminal state, including
// the paths that fail.
//
// Only the csv source is executed. github, jira and linear are enqueued
// by the API but have no connector: they finish as failed with a reason
// naming the gap, because a job that stops at pending tells the user
// nothing at all.
package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
)

// Defaults for the worker's cadence and limits.
const (
	// DefaultPollInterval is how often the worker looks for pending
	// jobs. Imports are user-initiated and the UI polls the row, so a
	// few seconds of pickup latency is not worth a tighter loop.
	DefaultPollInterval = 5 * time.Second
	// DefaultBatchSize caps how many jobs one pass claims.
	DefaultBatchSize = 5
	// DefaultStaleAfter is how long a running job may go without
	// finishing before the worker treats it as abandoned.
	DefaultStaleAfter = 30 * time.Minute
	// progressEvery is how many rows are created between progress
	// publications, which is also how often the run re-reads its own
	// status to notice a cancellation.
	progressEvery = 25
)

// staleReason is written to error_log when a job is reaped.
const staleReason = "import worker stopped before this job finished; the job was abandoned and no further tasks will be created"

// Worker claims pending import jobs and runs them to a terminal state.
type Worker struct {
	DB      *sql.DB
	Queries *generated.Queries
	Logger  *slog.Logger

	// PollInterval, BatchSize and StaleAfter fall back to their
	// Default* constants when left zero.
	PollInterval time.Duration
	BatchSize    int32
	StaleAfter   time.Duration

	stopOnce sync.Once
	stopCh   chan struct{}
	// doneOnce guards the close of done. The loop runs under bgloop,
	// which restarts it after a panic, so the teardown can execute more
	// than once over the life of the worker; closing a closed channel
	// would panic inside the very supervisor meant to contain panics.
	doneOnce sync.Once
	done     chan struct{}
}

// NewWorker builds a worker with the default cadence.
func NewWorker(db *sql.DB, q *generated.Queries, logger *slog.Logger) *Worker {
	return &Worker{
		DB:      db,
		Queries: q,
		Logger:  logger,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start runs the claim loop in its own supervised goroutine and
// returns.
//
// Supervised because a job row is the whole contract with the user: if
// one workspace's malformed file panics a pass, an unsupervised loop
// takes the process down, and every other tenant's import is left
// showing "still working" forever. bgloop contains the panic, records
// it, and restarts the loop.
func (w *Worker) Start(ctx context.Context) {
	go bgloop.Run(ctx, "importer.worker", w.Logger, w.loop)
	w.Logger.Info("import worker started",
		slog.Duration("interval", w.pollInterval()),
		slog.Duration("stale_after", w.staleAfter()),
	)
}

// Stop signals the loop to exit and waits for the in-flight pass.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	select {
	case <-w.done:
	case <-time.After(30 * time.Second):
		w.Logger.Warn("import worker: shutdown timeout exceeded; abandoning in-flight pass")
	}
}

func (w *Worker) loop(ctx context.Context) {
	defer w.doneOnce.Do(func() { close(w.done) })
	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				w.Logger.Error("import worker: pass failed", slog.String("err", err.Error()))
			}
		}
	}
}

// RunOnce reaps abandoned jobs, then claims and runs up to BatchSize
// pending ones. It returns the internal ids of the jobs this pass
// claimed, in the order they were run.
//
// The returned ids are what makes a caller able to tell "my job was
// processed" from "some job was processed". Claiming is exclusive, so an
// id in this slice was handled by this pass and no other — which a
// count of finished jobs, or a look at the queue afterwards, cannot
// establish when several workers share the table.
func (w *Worker) RunOnce(ctx context.Context) ([]uint32, error) {
	w.reapStale(ctx)

	pending, err := w.Queries.ListPendingImportJobs(ctx, w.batchSize())
	if err != nil {
		return nil, fmt.Errorf("importer: list pending jobs: %w", err)
	}

	claimed := make([]uint32, 0, len(pending))
	for _, job := range pending {
		if err := ctx.Err(); err != nil {
			return claimed, fmt.Errorf("importer: pass stopped after %d jobs: %w", len(claimed), err)
		}
		took, err := w.Queries.ClaimImportJob(ctx, job.ID)
		if err != nil {
			w.Logger.Error("import worker: claim failed",
				slog.Uint64("import_job_id", uint64(job.ID)),
				slog.String("err", err.Error()))
			continue
		}
		if took == 0 {
			// Another replica claimed it between the list and the
			// update. Not an error: the row is being handled.
			continue
		}
		claimed = append(claimed, job.ID)
		w.run(ctx, job)
	}
	return claimed, nil
}

// reapStale fails jobs whose worker died mid-import. Without it the row
// keeps the running status forever, which is the same never-finishing
// state this package exists to remove — only one step further along.
func (w *Worker) reapStale(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-w.staleAfter())
	n, err := w.Queries.FailStuckImportJobs(ctx, generated.FailStuckImportJobsParams{
		ErrorLog:  sql.NullString{String: staleReason, Valid: true},
		StartedAt: sql.NullTime{Time: cutoff, Valid: true},
	})
	if err != nil {
		w.Logger.Error("import worker: reap stale jobs", slog.String("err", err.Error()))
		return
	}
	if n > 0 {
		w.Logger.Warn("import worker: failed jobs abandoned by a previous run",
			slog.Int64("count", n),
			slog.Time("started_before", cutoff))
	}
}

// outcome accumulates what a run has to report back to the queue row.
type outcome struct {
	total     int
	processed int
	failed    int
	log       []string
}

func (o *outcome) note(format string, args ...any) {
	// The log column is TEXT and the UI renders it whole, so the tail
	// is capped rather than allowed to grow with the row count.
	const maxLines = 50
	if len(o.log) < maxLines {
		o.log = append(o.log, fmt.Sprintf(format, args...))
		return
	}
	if len(o.log) == maxLines {
		o.log = append(o.log, "further errors omitted")
	}
}

func (o *outcome) errorLog() sql.NullString {
	if len(o.log) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: strings.Join(o.log, "\n"), Valid: true}
}

// run executes one claimed job and leaves it in a terminal state.
func (w *Worker) run(ctx context.Context, job generated.ListPendingImportJobsRow) {
	log := w.Logger.With(
		slog.Uint64("import_job_id", uint64(job.ID)),
		slog.String("source", string(job.Source)),
	)

	var out outcome
	err := w.execute(ctx, job, &out, log)

	status := generated.ImportJobsStatusCompleted
	switch {
	case errors.Is(err, errCancelled):
		// The API already moved the row to cancelled; FinishImportJob
		// only matches running rows, so the counters are published on
		// their own and the status is left alone.
		w.publishProgress(ctx, job.ID, out, log)
		log.Info("import job cancelled while running",
			slog.Int("created", out.processed), slog.Int("failed", out.failed))
		return
	case err != nil:
		status = generated.ImportJobsStatusFailed
		out.note("%s", err.Error())
	case out.failed > 0 && out.processed == 0:
		// Nothing landed. Reporting that as completed would put a green
		// row in front of a user who got no tasks.
		status = generated.ImportJobsStatusFailed
	}

	if finishErr := w.Queries.FinishImportJob(ctx, generated.FinishImportJobParams{
		Status:         status,
		TotalItems:     uint32(max(out.total, 0)),     //#nosec G115 -- bounded by MaxCSVRows
		ProcessedItems: uint32(max(out.processed, 0)), //#nosec G115 -- bounded by MaxCSVRows
		FailedItems:    uint32(max(out.failed, 0)),    //#nosec G115 -- bounded by MaxCSVRows
		ErrorLog:       out.errorLog(),
		ID:             job.ID,
	}); finishErr != nil {
		// The row stays running and the reaper will terminate it later.
		log.Error("import worker: finish failed", slog.String("err", finishErr.Error()))
		return
	}

	w.announce(ctx, job, status, out, log)
}

// errCancelled unwinds a run whose job was cancelled through the API.
var errCancelled = errors.New("importer: job cancelled")

// execute dispatches on the job source. A source with no connector
// fails here rather than sitting in the queue.
func (w *Worker) execute(ctx context.Context, job generated.ListPendingImportJobsRow, out *outcome, log *slog.Logger) error {
	if job.Source != generated.ImportJobsSourceCsv {
		return fmt.Errorf("importer: the %s source is not implemented yet; only csv imports can run", job.Source)
	}
	if !job.ProjectID.Valid {
		return errors.New("importer: a csv import needs a target project; create the job with projectId set")
	}

	payload, err := csvPayload(job.ConfigJson)
	if err != nil {
		return err
	}
	rows, rowErrs, err := ParseCSV(payload)
	if err != nil {
		return err
	}

	out.total = len(rows) + len(rowErrs)
	out.failed = len(rowErrs)
	for _, re := range rowErrs {
		out.note("%s", re.Error())
	}

	projectID := uint32(job.ProjectID.Int32) //#nosec G115 -- projects.id, positive by construction
	for i, row := range rows {
		if i%progressEvery == 0 {
			if err := w.checkStillRunning(ctx, job.ID); err != nil {
				return err
			}
			w.publishProgress(ctx, job.ID, *out, log)
		}
		if err := w.createTask(ctx, job.WorkspaceID, projectID, row); err != nil {
			out.failed++
			out.note("line %d: %v", row.Line, err)
			continue
		}
		out.processed++
	}
	return nil
}

// createTask inserts one row's task through the canonical create path,
// so an imported task is indistinguishable from one made through the
// API: same task-number allocation, same defaults, same created event.
func (w *Worker) createTask(ctx context.Context, workspaceID, projectID uint32, row Row) error {
	return dbretry.InTx(ctx, w.DB, "importer.createTask", nil, func(ctx context.Context, tx *sql.Tx) error {
		created, err := taskcreate.New(ctx, tx, taskcreate.Args{
			WorkspaceID: workspaceID,
			ProjectID:   projectID,
			Title:       row.Title,
			Description: row.Description,
			Priority:    row.Priority,
			DueOn:       row.DueOn,
			StartedOn:   row.StartedOn,
		})
		if err != nil {
			return err
		}
		taskID := created.ID
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: workspaceID,
			TaskID:      &taskID,
			Payload: map[string]any{
				"taskId": created.PublicID.String(),
				"title":  row.Title,
				"source": "import",
			},
		})
	})
}

// checkStillRunning returns errCancelled once the API has moved the job
// out of running. Tasks already created stay: they are rows the user can
// see, and deleting them to make a cancellation look tidy would be a
// second destructive act on data the first one never promised to undo.
func (w *Worker) checkStillRunning(ctx context.Context, jobID uint32) error {
	status, err := w.Queries.FindImportJobStatusByID(ctx, jobID)
	if err != nil {
		// Treat an unreadable status as still running; the reaper is
		// the backstop if this job really has gone.
		return nil //nolint:nilerr // a transient read failure must not abort a run mid-import
	}
	if status != generated.ImportJobsStatusRunning {
		return errCancelled
	}
	return nil
}

func (w *Worker) publishProgress(ctx context.Context, jobID uint32, out outcome, log *slog.Logger) {
	if err := w.Queries.RecordImportJobProgress(ctx, generated.RecordImportJobProgressParams{
		TotalItems:     uint32(max(out.total, 0)),     //#nosec G115 -- bounded by MaxCSVRows
		ProcessedItems: uint32(max(out.processed, 0)), //#nosec G115 -- bounded by MaxCSVRows
		FailedItems:    uint32(max(out.failed, 0)),    //#nosec G115 -- bounded by MaxCSVRows
		ID:             jobID,
	}); err != nil {
		log.Warn("import worker: progress update failed", slog.String("err", err.Error()))
	}
}

// announce emits the terminal event for the job. The kinds already
// exist and had no producer until now.
func (w *Worker) announce(ctx context.Context, job generated.ListPendingImportJobsRow, status generated.ImportJobsStatus, out outcome, log *slog.Logger) {
	kind := eventbus.ImportJobCompleted
	if status == generated.ImportJobsStatusFailed {
		kind = eventbus.ImportJobFailed
	}
	eventbus.AppendBestEffort(ctx, w.DB, eventbus.Event{
		Type:        kind,
		WorkspaceID: job.WorkspaceID,
		Payload: map[string]any{
			"importJobId":    job.PublicID.String(),
			"source":         string(job.Source),
			"totalItems":     out.total,
			"processedItems": out.processed,
			"failedItems":    out.failed,
		},
	}, "importer.announce")
	log.Info("import job finished",
		slog.String("status", string(status)),
		slog.Int("total", out.total),
		slog.Int("created", out.processed),
		slog.Int("failed", out.failed),
	)
}

// csvPayload pulls the CSV text out of the job configuration.
//
// INTERIM: the payload rides inside config_json because there is no
// other channel for it. The create request carries no file and the job
// row has no storage_object reference, so a caller has nowhere else to
// put the bytes. The intended shape is an uploaded blob referenced by
// id, which the attachment presign flow already supports for tasks and
// events — adopting it here means changing the create DTO, the OpenAPI
// document, the web form and the MCP tool, which is why it is not done
// in the same change as the worker. Until then a queue row can hold up
// to MaxCSVBytes of user data, and the create handler rejects anything
// larger so the payload is never written at all.
func csvPayload(configJSON json.RawMessage) (string, error) {
	if len(configJSON) == 0 {
		return "", ErrNoCSVPayload
	}
	var cfg struct {
		CSV string `json:"csv"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return "", fmt.Errorf("importer: config_json is not an object: %w", err)
	}
	if strings.TrimSpace(cfg.CSV) == "" {
		return "", ErrNoCSVPayload
	}
	return cfg.CSV, nil
}

func (w *Worker) pollInterval() time.Duration {
	if w.PollInterval > 0 {
		return w.PollInterval
	}
	return DefaultPollInterval
}

func (w *Worker) batchSize() int32 {
	if w.BatchSize > 0 {
		return w.BatchSize
	}
	return DefaultBatchSize
}

func (w *Worker) staleAfter() time.Duration {
	if w.StaleAfter > 0 {
		return w.StaleAfter
	}
	return DefaultStaleAfter
}

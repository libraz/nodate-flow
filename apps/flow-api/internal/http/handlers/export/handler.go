package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

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

		var rows []exportRow
		var err error

		if in.LensID != "" {
			rows, err = fetchForLens(ctx, deps, ws, actorID, in.LensID, limit)
		} else {
			rows, err = fetchForWorkspace(ctx, deps, ws, actorID, limit)
		}
		if err != nil {
			return nil, err
		}

		tasks := mapRows(rows)

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

		var rows []exportRow
		var err error
		if in.LensID != "" {
			rows, err = fetchForLens(ctx, deps, ws, actorID, in.LensID, limit)
		} else {
			rows, err = fetchForWorkspace(ctx, deps, ws, actorID, limit)
		}
		if err != nil {
			return nil, err
		}
		tasks := mapRows(rows)

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "export.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "export",
			Metadata: map[string]any{
				"format": "csv",
				"count":  len(tasks),
			},
		})

		actorInt64 := int64(actorID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ExportRequested,
			WorkspaceID: ws.ID,
			ActorUserID: &actorInt64,
			Payload: map[string]any{
				"format": "csv",
				"count":  len(tasks),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "export.CSVOperation"),
				slog.String("event_type", string(eventbus.ExportRequested)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("format", "csv"),
			)
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
				hctx.SetHeader(RowCountHeader, strconv.Itoa(len(tasks)))
				hctx.SetStatus(http.StatusOK)
				writeCSV(hctx.BodyWriter(), tasks)
			},
		}, nil
	}
}

// writeCSV emits the export as CSV.
//
// The leading byte order mark is for spreadsheet software that reads a
// CSV as the local code page unless told otherwise; without it a file
// of non-ASCII task titles opens as mojibake. This is the only place
// that adds one — a second BOM written by a client assembling its own
// file would appear as stray characters in the first header cell.
func writeCSV(w io.Writer, tasks []ExportedTask) {
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"ID", "Title", "Description", "Status", "Priority",
		"Due Date", "Start Date", "Completed At",
		"Project ID", "Project", "Assignee ID", "Assignee",
		"Updated At", "Created At",
	})
	for _, t := range tasks {
		_ = cw.Write([]string{
			t.ID,
			t.Title,
			handlerutil.DerefStr(t.Description),
			t.Status,
			fmt.Sprintf("%d", t.Priority),
			handlerutil.DerefStr(t.DueOn),
			handlerutil.DerefStr(t.StartedOn),
			handlerutil.FormatOptionalUnix(t.CompletedAt),
			t.ProjectID,
			t.ProjectName,
			handlerutil.DerefStr(t.AssigneeID),
			handlerutil.DerefStr(t.AssigneeDisplayName),
			handlerutil.FormatOptionalUnix(t.UpdatedAt),
			handlerutil.FormatUnix(t.CreatedAt),
		})
	}
	cw.Flush()
}

// ----------------------------------------------------------------
// Internal helpers
// ----------------------------------------------------------------

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

// fetchForWorkspace fetches export rows across the entire workspace.
func fetchForWorkspace(
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, actorID uint32, limit int32,
) ([]exportRow, error) {
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
		return nil, datasetQueryFailed(ctx, "workspace", err)
	}
	out := make([]exportRow, len(dbRows))
	for i, r := range dbRows {
		out[i] = exportRow{
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
		}
	}
	return out, nil
}

// fetchForLens resolves the lens and fetches export rows scoped to its project.
func fetchForLens(
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, actorID uint32, lensID string, limit int32,
) ([]exportRow, error) {
	lid, err := types.Parse(lensID)
	if err != nil {
		return nil, httpErr(apierrors.ExportTaskLensNotFound)
	}

	projectID, err := deps.Queries.ResolveLensProjectID(ctx, generated.ResolveLensProjectIDParams{
		WorkspaceID: ws.ID,
		PublicID:    lid,
	})
	if err != nil {
		return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.ExportTaskLensNotFound, apierrors.ExportTaskDatasetQueryFailed))
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
			return nil, datasetQueryFailed(ctx, "lens", err)
		}
		out := make([]exportRow, len(dbRows))
		for i, r := range dbRows {
			out[i] = exportRow{
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
			}
		}
		return out, nil
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

// mapRows converts internal exportRow slices to the public DTO.
// All time/date conversions happen here and nowhere else.
func mapRows(rows []exportRow) []ExportedTask {
	tasks := make([]ExportedTask, 0, len(rows))
	for _, r := range rows {
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
		tasks = append(tasks, t)
	}
	return tasks
}

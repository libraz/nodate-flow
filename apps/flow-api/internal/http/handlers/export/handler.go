package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
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
func Export(deps Deps) func(ctx context.Context, in *ExportInput) (*ExportOutput, error) {
	return func(ctx context.Context, in *ExportInput) (*ExportOutput, error) {
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
			rows, err = fetchForLens(ctx, deps, ws, in.LensID, limit)
		} else {
			rows, err = fetchForWorkspace(ctx, deps, ws, limit)
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ExportRequested,
			WorkspaceID: ws.ID,
			ActorUserID: &actorInt64,
			Payload: map[string]any{
				"format": in.Format,
				"count":  len(tasks),
			},
		})

		return &ExportOutput{
			Body: ExportBody{
				Format: in.Format,
				Count:  len(tasks),
				Tasks:  tasks,
			},
		}, nil
	}
}

// ExportCSV returns a raw http.HandlerFunc that streams a CSV file.
// It is registered on the chi router directly (not via Huma) because
// the response is a file download with custom content-type headers.
func ExportCSV(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			http.Error(w, apierrors.WsWorkspaceNotFound.Code, http.StatusNotFound)
			return
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		q := r.URL.Query()
		lensID := q.Get("lensId")
		limit := parseLimit(q.Get("limit"), 5000)

		var rows []exportRow
		var err error

		if lensID != "" {
			rows, err = fetchForLens(ctx, deps, ws, lensID, limit)
		} else {
			rows, err = fetchForWorkspace(ctx, deps, ws, limit)
		}
		if err != nil {
			writeHTTPError(w, err)
			return
		}

		tasks := mapRows(rows)

		// Headers for file download.
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="tasks-export.csv"`)
		w.WriteHeader(http.StatusOK)

		// UTF-8 BOM for Excel compatibility.
		_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

		cw := csv.NewWriter(w)
		// Header row.
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
				derefStr(t.Description),
				t.Status,
				fmt.Sprintf("%d", t.Priority),
				derefStr(t.DueOn),
				derefStr(t.StartedOn),
				formatOptionalUnix(t.CompletedAt),
				t.ProjectID,
				t.ProjectName,
				derefStr(t.AssigneeID),
				derefStr(t.AssigneeDisplayName),
				formatOptionalUnix(t.UpdatedAt),
				formatUnix(t.CreatedAt),
			})
		}
		cw.Flush()

		// Audit log.
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

		// Append event.
		actorInt64 := int64(actorID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ExportRequested,
			WorkspaceID: ws.ID,
			ActorUserID: &actorInt64,
			Payload: map[string]any{
				"format": "csv",
				"count":  len(tasks),
			},
		})
	}
}

// ----------------------------------------------------------------
// Internal helpers
// ----------------------------------------------------------------

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
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, limit int32,
) ([]exportRow, error) {
	dbRows, err := deps.Queries.ExportTasksForWorkspace(ctx, generated.ExportTasksForWorkspaceParams{
		WorkspaceID: ws.ID,
		Limit:       limit,
	})
	if err != nil {
		return nil, httpErr(apierrors.ExportTaskGenerationFailed)
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
	ctx context.Context, deps Deps, ws middleware.WorkspaceContext, lensID string, limit int32,
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.ExportTaskLensNotFound)
		}
		return nil, httpErr(apierrors.ExportTaskGenerationFailed)
	}

	// If the lens has a project scope, use the project-scoped query.
	if projectID.Valid {
		dbRows, err := deps.Queries.ExportTasksForLens(ctx, generated.ExportTasksForLensParams{
			WorkspaceID: ws.ID,
			ProjectID:   uint32(projectID.Int32),
			Limit:       limit,
		})
		if err != nil {
			return nil, httpErr(apierrors.ExportTaskGenerationFailed)
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
	return fetchForWorkspace(ctx, deps, ws, limit)
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
			DueOn:       nullTimeToDateStr(r.DueOn),
			StartedOn:   nullTimeToDateStr(r.StartedOn),
			CompletedAt: nullTimeToUnix(r.CompletedAt),
			ProjectID:   r.ProjectPublicID.String(),
			ProjectName: r.ProjectName,
			UpdatedAt:   nullTimeToUnix(r.UpdatedAt),
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

// derefStr returns the string value or empty string for nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// formatUnix formats a unix seconds value as a decimal string.
func formatUnix(u int64) string {
	return fmt.Sprintf("%d", u)
}

// formatOptionalUnix formats an optional unix seconds value.
func formatOptionalUnix(u *int64) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf("%d", *u)
}

// parseLimit parses a limit string, returning the default on failure.
func parseLimit(s string, def int32) int32 {
	if s == "" {
		return def
	}
	var v int32
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil || v <= 0 {
		return def
	}
	if v > maxExportRows {
		return maxExportRows
	}
	return v
}

// writeHTTPError translates a huma error into a plain HTTP error response.
func writeHTTPError(w http.ResponseWriter, err error) {
	var he huma.StatusError
	if errors.As(err, &he) {
		http.Error(w, he.Error(), he.GetStatus())
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

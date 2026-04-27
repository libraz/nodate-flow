package tasks

import (
	"context"
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListMyTasks handles GET /me/tasks. It returns every task where the
// authenticated user is attached as an actor across every workspace
// they belong to, backed by the ListMyTasksGlobal sqlc query which
// joins v_my_tasks with workspaces so callers get workspace context
// per row without a second round-trip. Used by the web client's
// cross-workspace "Today" and Calendar views.
func ListMyTasks(deps Deps) func(context.Context, *ListMyTasksInput) (*ListMyTasksOutput, error) {
	return func(ctx context.Context, in *ListMyTasksInput) (*ListMyTasksOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		profile, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}

		out := &ListMyTasksOutput{}
		out.Body.Tasks = []MyTaskListItem{}

		// Keyset path — opt-in via non-empty ?cursor=. Cross-workspace,
		// no role gating because /me/tasks is inherently scoped to the
		// caller's own actor rows. Fetches limit+1 so the (limit+1)-th
		// row acts as a "has more" sentinel.
		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListMyTasksGlobalKeyset(ctx, generated.ListMyTasksGlobalKeysetParams{
				UserPublicID:    profile.PublicID,
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
			if hasMore {
				rows = rows[:limit]
			}
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, rowToMyTaskListItemKeyset(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}

		rows, err := deps.Queries.ListMyTasksGlobal(ctx, generated.ListMyTasksGlobalParams{
			UserPublicID: profile.PublicID,
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, rowToMyTaskListItem(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, nil
	}
}

// rowToMyTaskListItemKeyset is the keyset twin of rowToMyTaskListItem.
// The keyset row drops the Total column but otherwise has the same
// projection.
func rowToMyTaskListItemKeyset(r generated.ListMyTasksGlobalKeysetRow) MyTaskListItem {
	return MyTaskListItem{
		ID:            r.PublicID.String(),
		WorkspaceID:   r.WorkspacePublicID.String(),
		WorkspaceName: r.WorkspaceName,
		ProjectID:     bytesToUUIDString(r.ProjectPublicID),
		ProjectName:   r.ProjectName,
		Title:         r.Title,
		DerivedState:  string(r.DerivedState),
		Priority:      r.Priority,
		DueOn:         nullDate(r.DueOn),
		ActorRole:     string(r.ActorRole),
		UpdatedAt:     nullTimeUnix(r.UpdatedAt),
		CreatedAt:     r.CreatedAt.Unix(),
	}
}

func rowToMyTaskListItem(r generated.ListMyTasksGlobalRow) MyTaskListItem {
	return MyTaskListItem{
		ID:            r.PublicID.String(),
		WorkspaceID:   r.WorkspacePublicID.String(),
		WorkspaceName: r.WorkspaceName,
		ProjectID:     bytesToUUIDString(r.ProjectPublicID),
		ProjectName:   r.ProjectName,
		Title:         r.Title,
		DerivedState:  string(r.DerivedState),
		Priority:      r.Priority,
		DueOn:         nullDate(r.DueOn),
		ActorRole:     string(r.ActorRole),
		UpdatedAt:     nullTimeUnix(r.UpdatedAt),
		CreatedAt:     r.CreatedAt.Unix(),
	}
}

// ListMyTasksWithDates handles GET /me/tasks-with-dates?from=&to=. It
// returns tasks assigned to the authenticated user in any workspace
// whose due_on falls inside the requested inclusive date range. Pairs
// with GET /me/calendar-events so the unified flow-web calendar can
// render tasks and events with two requests instead of fanning out
// per-workspace.
func ListMyTasksWithDates(deps Deps) func(context.Context, *ListMyTasksWithDatesInput) (*ListMyTasksWithDatesOutput, error) {
	return func(ctx context.Context, in *ListMyTasksWithDatesInput) (*ListMyTasksWithDatesOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		profile, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		from, err := time.Parse("2006-01-02", in.From)
		if err != nil {
			return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
		}
		to, err := time.Parse("2006-01-02", in.To)
		if err != nil {
			return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
		}
		if to.Before(from) {
			return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 500
		}

		fromNT := sql.NullTime{Time: from, Valid: true}
		toNT := sql.NullTime{Time: to, Valid: true}

		out := &ListMyTasksWithDatesOutput{}

		// Keyset path — opt-in via non-empty ?cursor=. The keyset query
		// orders by created_at DESC, public_id DESC (NOT by due_on),
		// which differs from the OFFSET path's calendar ordering.
		// Callers wanting due-date ordering must keep using OFFSET.
		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListMyTasksWithDatesKeyset(ctx, generated.ListMyTasksWithDatesKeysetParams{
				UserPublicID:    profile.PublicID,
				FromDueOn:       fromNT,
				ToDueOn:         toNT,
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
			if hasMore {
				rows = rows[:limit]
			}
			out.Body.Tasks = make([]MyTaskListItem, 0, len(rows))
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, rowToMyTaskWithDatesItemKeyset(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}

		rows, err := deps.Queries.ListMyTasksWithDates(ctx, generated.ListMyTasksWithDatesParams{
			UserPublicID: profile.PublicID,
			FromDueOn:    fromNT,
			ToDueOn:      toNT,
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out.Body.Tasks = make([]MyTaskListItem, 0, len(rows))
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, rowToMyTaskWithDatesItem(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, nil
	}
}

// rowToMyTaskWithDatesItemKeyset mirrors rowToMyTaskWithDatesItem with
// the Total column dropped (keyset queries never carry a window total).
func rowToMyTaskWithDatesItemKeyset(r generated.ListMyTasksWithDatesKeysetRow) MyTaskListItem {
	return MyTaskListItem{
		ID:            r.PublicID.String(),
		WorkspaceID:   r.WorkspacePublicID.String(),
		WorkspaceName: r.WorkspaceName,
		ProjectID:     bytesToUUIDString(r.ProjectPublicID),
		ProjectName:   r.ProjectName,
		Title:         r.Title,
		DerivedState:  string(r.DerivedState),
		Priority:      r.Priority,
		DueOn:         nullDate(r.DueOn),
		ActorRole:     string(r.ActorRole),
		UpdatedAt:     nullTimeUnix(r.UpdatedAt),
		CreatedAt:     r.CreatedAt.Unix(),
	}
}

func rowToMyTaskWithDatesItem(r generated.ListMyTasksWithDatesRow) MyTaskListItem {
	return MyTaskListItem{
		ID:            r.PublicID.String(),
		WorkspaceID:   r.WorkspacePublicID.String(),
		WorkspaceName: r.WorkspaceName,
		ProjectID:     bytesToUUIDString(r.ProjectPublicID),
		ProjectName:   r.ProjectName,
		Title:         r.Title,
		DerivedState:  string(r.DerivedState),
		Priority:      r.Priority,
		DueOn:         nullDate(r.DueOn),
		ActorRole:     string(r.ActorRole),
		UpdatedAt:     nullTimeUnix(r.UpdatedAt),
		CreatedAt:     r.CreatedAt.Unix(),
	}
}

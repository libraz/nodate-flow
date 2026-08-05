package activity

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// List handles GET /workspaces/{wsId}/activity. It returns a cursor-paginated
// page of the unified workspace activity feed (audit + AI + MCP), newest first.
//
// Access control is enforced by the surrounding chi group, which layers
// RequireAuth + RequireWorkspaceMember before this handler runs, so the
// workspace id from context is trusted. The workspace_id column is always
// part of the WHERE clause (via the sqlc-generated query), so a caller
// authorised for workspace A can never observe workspace B's activity.
//
// Pagination follows the keyset idiom used elsewhere (see tasks.ListComments):
// the query asks for limit+1 rows; when the surplus row is present a
// nextCursor is encoded from the last in-page row's (occurred_at, public_id)
// tuple. total comes from a separate COUNT over the same filters.
func List(deps Deps) func(context.Context, *ListActivityInput) (*ListActivityOutput, error) {
	return func(ctx context.Context, in *ListActivityInput) (*ListActivityOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			// RequireWorkspaceMember always injects the workspace before this
			// handler is reached; a missing context indicates a routing
			// misconfiguration, not a missing resource.
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
		if derr != nil {
			return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
		}

		out := &ListActivityOutput{}
		out.Body.Activity = []Entry{}

		total, err := deps.Queries.CountWorkspaceActivity(ctx, generated.CountWorkspaceActivityParams{
			WorkspaceID:  ws.ID,
			FilterSource: in.Source,
			FilterSince:  sql.NullTime{},
			FilterUntil:  sql.NullTime{},
		})
		if err != nil {
			slog.ErrorContext(ctx, "activity count query failed", slog.String("err", err.Error()), logutil.LogEntity("workspace", ws.PublicID))
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out.Body.Total = total

		rows, err := deps.Queries.ListWorkspaceActivity(ctx, generated.ListWorkspaceActivityParams{
			WorkspaceID:      ws.ID,
			FilterSource:     in.Source,
			FilterSince:      sql.NullTime{},
			FilterUntil:      sql.NullTime{},
			CursorOccurredAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
			CursorPublicID:   cursorPID,
			Limit:            limit + 1,
		})
		if err != nil {
			slog.ErrorContext(ctx, "activity list query failed", slog.String("err", err.Error()), logutil.LogEntity("workspace", ws.PublicID))
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
		if hasMore {
			rows = rows[:limit]
		}
		out.Body.Activity = make([]Entry, 0, len(rows))
		for _, r := range rows {
			out.Body.Activity = append(out.Body.Activity, mapRow(r))
		}
		if hasMore {
			last := rows[len(rows)-1]
			nc := handlerutil.EncodeCursor(last.OccurredAt, last.PublicID)
			out.Body.NextCursor = &nc
		}
		return out, nil
	}
}

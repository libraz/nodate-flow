package audit

import (
	"context"
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// List handles GET /workspaces/{wsId}/audit-logs. It returns the most
// recent workspace audit entries with optional action / resource_type /
// actor-search / date-range filters, paginated by limit and offset.
//
// Access control is enforced by the surrounding chi group, which layers
// RequireAuth + RequireWorkspaceMember + RequireWorkspaceRole(admin)
// before this handler runs. The handler therefore trusts the workspace
// id and actor from context.
//
// The workspace_id column is always part of the WHERE clause (via the
// sqlc-generated query) so a caller authorised for workspace A can
// never observe workspace B's audit trail.
func List(deps Deps) func(context.Context, *ListAuditLogsInput) (*ListAuditLogsOutput, error) {
	return func(ctx context.Context, in *ListAuditLogsInput) (*ListAuditLogsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			// RequireWorkspaceMember always injects the workspace before
			// this handler is reached; if the context is missing it
			// indicates a routing misconfiguration, not a missing resource.
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		params := generated.ListWorkspaceAuditLogsParams{
			WorkspaceID:        ws.ID,
			FilterAction:       in.Action,
			FilterResourceType: in.ResourceType,
			// sqlc emits interface{} for this parameter because the
			// same argument is reused in both an "= ''" comparison and
			// a LIKE CONCAT — Go's mysql driver accepts plain strings
			// on interface{} fields, so pass the raw value.
			FilterActorSearch: in.ActorSearch,
			FilterFrom:        parseDateFrom(in.DateFrom),
			FilterTo:          parseDateTo(in.DateTo),
			Limit:             in.Limit,
			Offset:            in.Offset,
		}

		rows, err := deps.Queries.ListWorkspaceAuditLogs(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListAuditLogsOutput{}
		out.Body.Entries = make([]AuditLogEntryDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Entries = append(out.Body.Entries, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// parseDateFrom interprets a YYYY-MM-DD calendar date as the inclusive
// lower bound on occurred_at, anchored at UTC midnight. An empty input
// disables the filter (sql.NullTime{Valid: false}).
func parseDateFrom(s string) sql.NullTime {
	if s == "" {
		return sql.NullTime{}
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		// Silently drop malformed filters: Huma validates the struct
		// tags but not the date shape, and we prefer "no filter" over
		// 400-ing on every stray query string. The audit log page is
		// read-only, so a permissive fallback cannot cause damage.
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// parseDateTo interprets a YYYY-MM-DD calendar date as the inclusive
// upper bound on occurred_at. The bound is extended to 23:59:59.999999999
// UTC so the final day of the requested range is included.
func parseDateTo(s string) sql.NullTime {
	if s == "" {
		return sql.NullTime{}
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return sql.NullTime{}
	}
	t = t.Add(24*time.Hour - time.Nanosecond)
	return sql.NullTime{Time: t, Valid: true}
}

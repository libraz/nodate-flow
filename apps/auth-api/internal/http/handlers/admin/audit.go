package admin

import (
	"context"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// ListAuditLogs handles GET /admin/audit-logs. Returns a paginated list of
// instance-level audit log entries with optional action and date-range filters.
func ListAuditLogs(deps Deps) func(context.Context, *ListAuditLogsInput) (*ListAuditLogsOutput, error) {
	return func(ctx context.Context, in *ListAuditLogsInput) (*ListAuditLogsOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		// Convert optional unix-second boundaries to time.Time for the query.
		// When nil, pass nil as the interface{} so the WHERE clause skips the
		// bound (see "? IS NULL OR ..." in the SQL).
		var fromFilter interface{}
		var fromTime time.Time
		if in.From != nil {
			fromTime = time.Unix(*in.From, 0)
			fromFilter = fromTime
		}

		var toFilter interface{}
		var toTime time.Time
		if in.To != nil {
			toTime = time.Unix(*in.To, 0)
			toFilter = toTime
		}

		rows, err := deps.Queries.AdminListInstanceAuditLogs(ctx, generated.AdminListInstanceAuditLogsParams{
			Column1:      in.Action,
			Action:       in.Action,
			Column3:      fromFilter,
			OccurredAt:   fromTime,
			Column5:      toFilter,
			OccurredAt_2: toTime,
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListAuditLogsOutput{}
		out.Body.Items = make([]AuditEntry, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToAuditEntry(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

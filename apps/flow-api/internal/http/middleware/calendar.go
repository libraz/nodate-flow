package middleware

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// RequireCalendarMember returns a middleware that resolves {calId} from the
// URL and refuses a caller who holds no active grant on that calendar.
//
// Membership is calendar_members, not calendar_subscriptions: a
// subscription is one user's display preference and grants nothing, so
// reading it here would let anyone who once toggled a sidebar colour reach
// a calendar they were never given.
//
// This gate proves membership only, and it publishes nothing: every
// handler behind it resolves the calendar again through
// resolveCalendar / resolveCalendarWrite, because those also apply the
// role floor, which differs per endpoint and which a middleware cannot see
// from where it sits. The internal id and grant used to be written to the
// request context for handlers to read; nothing ever read them.
//
// The lookup is one statement, and it is scoped by {wsId}. Two statements
// with no workspace predicate meant a calendar in another workspace could
// pass this gate on a membership row and be rejected one layer later — a
// correct outcome reached the long way, twice per request.
func RequireCalendarMember(db ACLDB) func(http.Handler) http.Handler {
	// LEFT JOIN rather than INNER: the two failure modes answer
	// differently, so membership has to come back as a nullable column
	// instead of as a missing row.
	const gateQuery = `SELECT IF(m.id IS NULL, 0, 1)
FROM calendars c
INNER JOIN workspaces w
  ON w.id = c.workspace_id AND w.public_id = ? AND w.enabled = TRUE
LEFT JOIN calendar_members m
  ON m.calendar_id = c.id AND m.user_id = ? AND m.enabled = TRUE
WHERE c.public_id = ? AND c.enabled = TRUE
LIMIT 1`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actorID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.CalendarCalendarAccessDenied)
				return
			}

			wsPub, err := uuid.Parse(chi.URLParam(r, "wsId"))
			if err != nil {
				writeSpecError(w, apierrors.CalendarCalendarNotFound)
				return
			}
			calPub, err := uuid.Parse(chi.URLParam(r, "calId"))
			if err != nil {
				writeSpecError(w, apierrors.CalendarCalendarNotFound)
				return
			}

			var isMember int
			if err := db.QueryRowContext(r.Context(), gateQuery,
				types.FromUUID(wsPub), actorID, types.FromUUID(calPub),
			).Scan(&isMember); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeSpecError(w, apierrors.CalendarCalendarNotFound)
					return
				}
				writeSpecError(w, apierrors.InternalUnexpected)
				return
			}
			if isMember == 0 {
				writeSpecError(w, apierrors.CalendarCalendarAccessDenied)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

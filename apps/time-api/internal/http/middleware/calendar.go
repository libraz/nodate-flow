package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
)

// CalendarContext is the calendar metadata injected by RequireCalendarMember.
type CalendarContext struct {
	ID       uint32
	PublicID uuid.UUID
}

// CalendarFromContext extracts the calendar metadata established by
// RequireCalendarMember.
func CalendarFromContext(ctx context.Context) (CalendarContext, bool) {
	id, ok := ctx.Value(ctxKeyCalendarID).(uint32)
	if !ok {
		return CalendarContext{}, false
	}
	pub, _ := ctx.Value(ctxKeyCalendarIDPublic).(uuid.UUID)
	return CalendarContext{ID: id, PublicID: pub}, true
}

// RequireCalendarMember returns a middleware that resolves {calId} from the
// URL, verifies the actor is a member of the calendar, and stores the
// internal calendar id on the request context.
//
// This is a placeholder that performs the lookup but does not yet check
// membership in a calendar_members table. The real implementation will be
// wired once the database schema is defined.
func RequireCalendarMember(db ACLDB) func(http.Handler) http.Handler {
	const calQuery = `SELECT id FROM calendars WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, apierrors.CalendarAccessDenied.Code,
					apierrors.CalendarAccessDenied.Message)
				return
			}
			raw := chi.URLParam(r, "calId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, apierrors.CalendarNotFound.Code,
					apierrors.CalendarNotFound.Message)
				return
			}
			var calID uint32
			if err := db.QueryRowContext(r.Context(), calQuery, types.FromUUID(pub)).Scan(&calID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, apierrors.CalendarNotFound.Code,
						apierrors.CalendarNotFound.Message)
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyCalendarID, calID)
			ctx = context.WithValue(ctx, ctxKeyCalendarIDPublic, pub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

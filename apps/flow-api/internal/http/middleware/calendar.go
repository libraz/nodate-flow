package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// calCtxKey is an unexported type used as a context key for
// calendar-specific middleware values.
type calCtxKey int

const (
	ctxKeyCalendarID calCtxKey = iota
	ctxKeyCalendarIDPublic
	ctxKeyCalendarMember
)

// CalendarContext is the calendar metadata injected by RequireCalendarMember.
type CalendarContext struct {
	ID       uint32
	PublicID uuid.UUID
}

// MemberContext holds the caller's grant on the resolved calendar. It is
// populated by RequireCalendarMember.
type MemberContext struct {
	ID uint32
	// Role is the calendar_members.role value: owner, manager, editor or
	// viewer. The middleware only proves membership; a handler that needs
	// more than read access checks this.
	Role string
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

// MemberFromContext extracts the caller's calendar grant established by
// RequireCalendarMember. Returns false when the middleware has not run.
func MemberFromContext(ctx context.Context) (MemberContext, bool) {
	v, ok := ctx.Value(ctxKeyCalendarMember).(MemberContext)
	return v, ok
}

// RequireCalendarMember returns a middleware that resolves {calId} from the
// URL, verifies the actor holds an active grant on the calendar, and stores
// the internal calendar id and the grant on the request context.
//
// Membership is calendar_members, not calendar_subscriptions: a
// subscription is one user's display preference and grants nothing, so
// reading it here would let anyone who once toggled a sidebar colour reach
// a calendar they were never given.
//
// This gate proves membership only. Role floors live in the handlers,
// because what counts as enough differs per endpoint and a middleware
// cannot see which one it is fronting.
func RequireCalendarMember(db ACLDB) func(http.Handler) http.Handler {
	const calQuery = `SELECT id FROM calendars WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const memberQuery = `SELECT id, role FROM calendar_members WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actorID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.CalendarCalendarAccessDenied)
				return
			}

			raw := chi.URLParam(r, "calId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeSpecError(w, apierrors.CalendarCalendarNotFound)
				return
			}

			var calID uint32
			if err := db.QueryRowContext(r.Context(), calQuery, types.FromUUID(pub)).Scan(&calID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeSpecError(w, apierrors.CalendarCalendarNotFound)
					return
				}
				writeSpecError(w, apierrors.InternalUnexpected)
				return
			}

			var memberID uint32
			var role string
			if err := db.QueryRowContext(r.Context(), memberQuery, calID, actorID).Scan(&memberID, &role); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeSpecError(w, apierrors.CalendarCalendarAccessDenied)
					return
				}
				writeSpecError(w, apierrors.InternalUnexpected)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyCalendarID, calID)
			ctx = context.WithValue(ctx, ctxKeyCalendarIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyCalendarMember, MemberContext{ID: memberID, Role: role})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

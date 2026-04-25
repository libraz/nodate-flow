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
	ctxKeySubscription
)

// CalendarContext is the calendar metadata injected by RequireCalendarMember.
type CalendarContext struct {
	ID       uint32
	PublicID uuid.UUID
}

// SubscriptionContext holds the caller's subscription (membership) info for the
// resolved calendar. It is populated by RequireCalendarMember.
type SubscriptionContext struct {
	ID uint32
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

// SubscriptionFromContext extracts the caller's subscription info established
// by RequireCalendarMember. Returns false when the middleware has not run or
// the user has no subscription.
func SubscriptionFromContext(ctx context.Context) (SubscriptionContext, bool) {
	v, ok := ctx.Value(ctxKeySubscription).(SubscriptionContext)
	return v, ok
}

// RequireCalendarMember returns a middleware that resolves {calId} from the
// URL, verifies the actor holds an active subscription to the calendar, and
// stores both the internal calendar id and the subscription info on the
// request context.
func RequireCalendarMember(db ACLDB) func(http.Handler) http.Handler {
	const calQuery = `SELECT id FROM calendars WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const subQuery = `SELECT id FROM calendar_subscriptions WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actorID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, apierrors.CalendarCalendarAccessDenied.Code,
					apierrors.CalendarCalendarAccessDenied.Message)
				return
			}

			raw := chi.URLParam(r, "calId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, apierrors.CalendarCalendarNotFound.Code,
					apierrors.CalendarCalendarNotFound.Message)
				return
			}

			var calID uint32
			if err := db.QueryRowContext(r.Context(), calQuery, types.FromUUID(pub)).Scan(&calID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, apierrors.CalendarCalendarNotFound.Code,
						apierrors.CalendarCalendarNotFound.Message)
					return
				}
				writeError(w, http.StatusInternalServerError, apierrors.InternalUnexpected.Code, apierrors.InternalUnexpected.Message)
				return
			}

			// Verify the actor has an active subscription (membership).
			var subID uint32
			if err := db.QueryRowContext(r.Context(), subQuery, calID, actorID).Scan(&subID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden, apierrors.CalendarCalendarAccessDenied.Code,
						apierrors.CalendarCalendarAccessDenied.Message)
					return
				}
				writeError(w, http.StatusInternalServerError, apierrors.InternalUnexpected.Code, apierrors.InternalUnexpected.Message)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyCalendarID, calID)
			ctx = context.WithValue(ctx, ctxKeyCalendarIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeySubscription, SubscriptionContext{ID: subID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

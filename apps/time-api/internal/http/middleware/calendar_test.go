package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalendarFromContext_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cal, ok := CalendarFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, CalendarContext{}, cal)
}

func TestCalendarFromContext_WithValues(t *testing.T) {
	t.Parallel()
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyCalendarID, uint32(42))
	ctx = context.WithValue(ctx, ctxKeyCalendarIDPublic, pub)

	cal, ok := CalendarFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, uint32(42), cal.ID)
	assert.Equal(t, pub, cal.PublicID)
}

func TestSubscriptionFromContext_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sub, ok := SubscriptionFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, SubscriptionContext{}, sub)
}

func TestSubscriptionFromContext_WithValues(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), ctxKeySubscription, SubscriptionContext{
		ID: 7,
	})

	sub, ok := SubscriptionFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, uint32(7), sub.ID)
}

// TestRequireCalendarMember_NoActor verifies that requests without an
// authenticated actor get a 403 response.
func TestRequireCalendarMember_NoActor(t *testing.T) {
	t.Parallel()

	mw := RequireCalendarMember(nil) // db is not needed; actor check comes first
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := chi.NewRouter()
	r.Route("/{calId}", func(sub chi.Router) {
		sub.Use(func(next http.Handler) http.Handler { return handler })
		sub.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "CALENDAR.CALENDAR.ACCESS_DENIED")
}

// TestRequireCalendarMember_InvalidUUID verifies that a malformed calId
// returns 404.
func TestRequireCalendarMember_InvalidUUID(t *testing.T) {
	t.Parallel()

	mw := RequireCalendarMember(nil) // db not reached
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := chi.NewRouter()
	r.Route("/{calId}", func(sub chi.Router) {
		sub.Use(func(next http.Handler) http.Handler { return handler })
		sub.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/not-a-uuid", nil)
	req = req.WithContext(WithActor(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "CALENDAR.CALENDAR.NOT_FOUND")
}

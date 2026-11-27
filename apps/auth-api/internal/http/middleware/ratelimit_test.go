package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

func TestAPIRateLimiter_UsersBehindOneIPDoNotShareBucket(t *testing.T) {
	t.Parallel()

	rl := NewAPIRateLimiter(APIRateLimitConfig{AuthedMaxRequests: 2, Window: time.Minute})
	t.Cleanup(rl.Stop)
	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	serve := func(user uint32) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := authn.WithClientIP(req.Context(), "203.0.113.7")
		ctx = authn.WithActor(ctx, user)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}

	require.Equal(t, http.StatusOK, serve(1))
	require.Equal(t, http.StatusOK, serve(1))
	require.Equal(t, http.StatusTooManyRequests, serve(1), "user 1 exhausts only their own bucket")
	require.Equal(t, http.StatusOK, serve(2), "user 2 on the same IP is unaffected")
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/stretchr/testify/require"
)

func TestIPRateLimiter_Middleware_AllowsUnderLimit(t *testing.T) {
	t.Parallel()

	rl := NewIPRateLimiter(RateLimitConfig{
		MaxRequests: 5,
		Window:      time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Send 5 requests (exactly at the limit) — all should pass.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := WithClientIP(req.Context(), "10.0.0.1")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d should be allowed", i+1)
		require.NotEmpty(t, rec.Header().Get("X-RateLimit-Limit"), "X-RateLimit-Limit must be set")
		require.NotEmpty(t, rec.Header().Get("X-RateLimit-Remaining"), "X-RateLimit-Remaining must be set")
		require.NotEmpty(t, rec.Header().Get("X-RateLimit-Reset"), "X-RateLimit-Reset must be set")
	}
}

func TestIPRateLimiter_Middleware_BlocksOverLimit(t *testing.T) {
	t.Parallel()

	rl := NewIPRateLimiter(RateLimitConfig{
		MaxRequests: 3,
		Window:      time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ip := "192.168.1.100"

	// Exhaust the limit.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := WithClientIP(req.Context(), ip)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d should pass", i+1)
	}

	// The 4th request should be rejected with 429.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClientIP(req.Context(), ip)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, "over-limit request must be 429")
	require.NotEmpty(t, rec.Header().Get("Retry-After"), "Retry-After header must be set")
	require.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"), "Remaining must be 0")
}

func TestIPRateLimiter_Middleware_DifferentIPsAreIndependent(t *testing.T) {
	t.Parallel()

	rl := NewIPRateLimiter(RateLimitConfig{
		MaxRequests: 1,
		Window:      time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First IP uses its single slot.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClientIP(req.Context(), "10.0.0.1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Second IP should still be allowed (separate bucket).
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx2 := WithClientIP(req2.Context(), "10.0.0.2")
	req2 = req2.WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	// First IP should now be blocked.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx3 := WithClientIP(req3.Context(), "10.0.0.1")
	req3 = req3.WithContext(ctx3)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusTooManyRequests, rec3.Code)
}

func TestIPRateLimiter_Middleware_NoClientIP_PassesThrough(t *testing.T) {
	t.Parallel()

	rl := NewIPRateLimiter(RateLimitConfig{
		MaxRequests: 1,
		Window:      time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No ClientIP in context — should pass through without rate limiting.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d with no IP should pass", i+1)
	}
}

func TestIPRateLimiter_Stop(t *testing.T) {
	t.Parallel()

	t.Run("first call does not panic", func(t *testing.T) {
		t.Parallel()
		rl := NewIPRateLimiter(RateLimitConfig{
			MaxRequests: 10,
			Window:      time.Second,
		})
		require.NotPanics(t, func() { rl.Stop() })
	})

	t.Run("multiple calls do not panic", func(t *testing.T) {
		t.Parallel()
		rl := NewIPRateLimiter(RateLimitConfig{
			MaxRequests: 10,
			Window:      time.Second,
		})
		require.NotPanics(t, func() {
			rl.Stop()
			rl.Stop()
			rl.Stop()
		})
	})
}

func TestAPIRateLimiter_Middleware_AuthedUser(t *testing.T) {
	t.Parallel()

	rl := NewAPIRateLimiter(APIRateLimitConfig{
		AuthedMaxRequests:   3,
		UnauthedMaxRequests: 1,
		Window:              time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Authenticated user gets the higher limit.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := authn.WithActor(req.Context(), 42)
		ctx = WithClientIP(ctx, "10.0.0.1")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "authed request %d should pass", i+1)
		require.Equal(t, "3", rec.Header().Get("X-RateLimit-Limit"))
	}

	// 4th request from same user should be blocked.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := authn.WithActor(req.Context(), 42)
	ctx = WithClientIP(ctx, "10.0.0.1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestAPIRateLimiter_Middleware_UnauthedFallsToIP(t *testing.T) {
	t.Parallel()

	rl := NewAPIRateLimiter(APIRateLimitConfig{
		AuthedMaxRequests:   100,
		UnauthedMaxRequests: 2,
		Window:              time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated requests use the lower IP-based limit.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := WithClientIP(req.Context(), "10.0.0.1")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "2", rec.Header().Get("X-RateLimit-Limit"))
	}

	// 3rd unauthenticated request should be blocked.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClientIP(req.Context(), "10.0.0.1")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestAPIRateLimiter_Middleware_NoIPNoAuth_PassesThrough(t *testing.T) {
	t.Parallel()

	rl := NewAPIRateLimiter(APIRateLimitConfig{
		UnauthedMaxRequests: 1,
		Window:              time.Minute,
	})
	t.Cleanup(rl.Stop)

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No auth, no IP — should pass through.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
}

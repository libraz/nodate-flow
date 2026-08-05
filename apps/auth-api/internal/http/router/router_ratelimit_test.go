package router

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// rateLimitDeps mirrors stubDeps but with rate-limiting enabled and a
// tight auth bucket, so a small handful of requests is enough to trip
// the limiter. The DB pointer is unreachable on purpose: every request
// in this test is rejected at either the rate limiter or the handler's
// token-lookup before we'd need a real database, so the only thing we
// observe is the HTTP status code.
func rateLimitDeps(t *testing.T, authMax int) Deps {
	t.Helper()
	issuer, err := auth.NewJWTIssuer(nil, "nodate-auth", "api", 15*time.Minute)
	require.NoError(t, err)
	db, err := sql.Open("mysql", "stub:stub@tcp(127.0.0.1:1)/stub?timeout=1ms")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return Deps{
		DB:                        db,
		Queries:                   generated.New(db),
		JWT:                       issuer,
		DisableRateLimit:          false,
		RateLimitGlobalMax:        10000,
		RateLimitGlobalWindowSec:  60,
		RateLimitAuthMax:          authMax,
		RateLimitAuthWindowSec:    60,
		RateLimitSessionMax:       10000,
		RateLimitSessionWindowSec: 60,
	}
}

// TestMagicLinkVerify_RateLimitedReturns429 hammers the magic-link
// verify endpoint past the per-IP auth bucket and asserts the limiter
// kicks in with 429 Too Many Requests. This is the brute-force
// protection on the verify path: even if an attacker knows the URL
// shape, they get rate-limited per IP. The exact limit is governed by
// NF_AUTH_RATE_LIMIT_AUTH_MAX (production default 20 / 15min); we set
// it to a tiny value here so the test runs quickly without sleep.
func TestMagicLinkVerify_RateLimitedReturns429(t *testing.T) {
	t.Parallel()
	const authMax = 3
	h := Build(rateLimitDeps(t, authMax))

	// Drive enough requests to exceed the bucket. Token is intentionally
	// junk: we only care about the rate-limit middleware response, never
	// reaching the handler. The first authMax responses may be any 4xx
	// (handler will return 401 for a malformed token), the rest must be
	// exactly 429.
	const totalRequests = authMax + 5
	statuses := make([]int, 0, totalRequests)
	for i := 0; i < totalRequests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/auth/magic-link/verify?token=bogus", nil)
		req.RemoteAddr = "203.0.113.7:54321" // fixed IP so all requests share the bucket
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		statuses = append(statuses, rec.Code)
	}

	got429 := 0
	for _, s := range statuses {
		if s == http.StatusTooManyRequests {
			got429++
		}
	}
	assert.GreaterOrEqual(t, got429, totalRequests-authMax,
		"once the auth bucket is full, every later verify request must "+
			"get 429; statuses observed: %v", statuses)
	assert.Equal(t, http.StatusTooManyRequests, statuses[totalRequests-1],
		"the last request must be rejected with 429")

	lastReq := httptest.NewRequest(http.MethodGet, "/auth/magic-link/verify?token=bogus", nil)
	lastReq.RemoteAddr = "203.0.113.7:54321"
	lastRec := httptest.NewRecorder()
	h.ServeHTTP(lastRec, lastReq)
	require.Equal(t, http.StatusTooManyRequests, lastRec.Code)
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(lastRec.Body).Decode(&body))
	assert.Equal(t, apierr.CodeRateLimitExceeded, body.Code)
}

// TestMagicLinkVerify_IsBehindAuthRateLimitGroup is a structural check:
// /auth/magic-link/verify must live inside the rate-limited group along
// with /auth/login and /auth/register. Without this guard, someone
// could split routes into a separate Group without rate-limiting and
// silently undo the brute-force protection. We verify by hammering
// /auth/login at the same shared bucket and observing that hits to
// /auth/magic-link/verify count toward the same per-IP cap.
func TestMagicLinkVerify_IsBehindAuthRateLimitGroup(t *testing.T) {
	t.Parallel()
	const authMax = 2
	h := Build(rateLimitDeps(t, authMax))

	const ip = "198.51.100.42:1234"

	// Fill the auth bucket using /auth/magic-link/verify hits.
	for i := 0; i < authMax; i++ {
		req := httptest.NewRequest(http.MethodGet, "/auth/magic-link/verify?token=x", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"requests within the bucket must not be 429 (got %d on iter %d)", rec.Code, i)
	}

	// Now /auth/login from the same IP must already be 429 because both
	// endpoints share the same per-IP auth bucket. If verify lived
	// outside the group, login would still have a fresh bucket and
	// return its usual 4xx — exposing the regression.
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	loginReq.RemoteAddr = ip
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusTooManyRequests, loginRec.Code,
		"verify and login must share the auth rate-limit bucket; "+
			"hits to verify must count against login's quota")
}

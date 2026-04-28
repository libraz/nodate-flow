package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

// stubRefreshSessions is a [sessionstore.Store] tailored to the refresh
// handler's reads. The handler only calls FindByRefreshHash before the
// idle-timeout gate, so the other methods panic to flag drift.
type stubRefreshSessions struct {
	session *sessionstore.Session
	err     error
}

func (s *stubRefreshSessions) Create(_ context.Context, _ sessionstore.CreateParams) (uint32, error) {
	panic("not implemented")
}

func (s *stubRefreshSessions) FindByRefreshHash(_ context.Context, _ string) (*sessionstore.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}

func (s *stubRefreshSessions) RotateRefreshHash(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func (s *stubRefreshSessions) Revoke(_ context.Context, _ uint32, _ dbtype.PublicID) error {
	panic("not implemented")
}

func (s *stubRefreshSessions) ListActive(_ context.Context, _ uint32) ([]sessionstore.Session, error) {
	panic("not implemented")
}

func (s *stubRefreshSessions) RevokeAllExcept(_ context.Context, _ uint32, _ dbtype.PublicID) error {
	panic("not implemented")
}

// TestRefresh_RejectsIdleSessionPastTimeout is the regression for L2:
// a session whose last_used_at is older than [sessionIdleTimeout] must
// fail refresh with TOKEN.REFRESH_EXPIRED, even when the wall-clock
// expires_at is still in the future. This stops a stolen refresh
// cookie sitting on a parked device from quietly minting access tokens
// for the remainder of the cookie TTL.
func TestRefresh_RejectsIdleSessionPastTimeout(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	idleStart := time.Now().Add(-(sessionIdleTimeout + 24*time.Hour))
	sess := &sessionstore.Session{
		InternalID: 1,
		PublicID:   dbtype.New(),
		UserID:     7,
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour), // wall clock still valid
		LastUsedAt: &idleStart,
		CreatedAt:  idleStart.Add(-time.Hour),
	}
	deps := Deps{
		JWT:      jwt,
		Sessions: &stubRefreshSessions{session: sess},
	}

	_, err = Refresh(deps)(context.Background(), &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "anything"},
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthTokenRefreshExpired.Code, problem.Type,
		"idle-timed-out session must surface as TOKEN.REFRESH_EXPIRED")
}

// TestRefresh_AcceptsActiveSession is the happy-path counterpart: a
// session used recently must pass the idle-timeout gate. Without this
// the L2 fix would lock everyone out on the next refresh.
//
// We do not exercise the post-gate code paths (RotateRefreshHash, JWT
// re-sign, FindUserPublicIdById) — those need a real DB and are
// already covered by the handler's existing integration tests. The
// assertion is simply that the request gets past the new gate; we let
// the handler proceed and surface whichever error first occurs from
// the un-stubbed Queries.
func TestRefresh_AcceptsActiveSession(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	recent := time.Now().Add(-2 * time.Hour)
	sess := &sessionstore.Session{
		InternalID: 1,
		PublicID:   dbtype.New(),
		UserID:     7,
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour),
		LastUsedAt: &recent,
		CreatedAt:  recent.Add(-time.Hour),
	}
	// Queries is nil — the handler will panic when it reaches
	// FindUserPublicIdById. That's the assertion: we got past both
	// the wall-clock and idle-timeout gates without short-circuiting.
	deps := Deps{
		JWT:      jwt,
		Sessions: &stubRefreshSessions{session: sess},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected nil-Queries to panic past the idle-timeout gate")
		}
	}()
	_, _ = Refresh(deps)(context.Background(), &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "anything"},
	})
}

// TestRefresh_FreshSessionWithNilLastUsedFallsBackToCreatedAt covers
// the just-minted-session edge case: LastUsedAt is nil until the first
// rotation, so the gate must fall back to CreatedAt to avoid
// erroneously rejecting a brand-new session that happens to be the
// first refresh after sign-in.
func TestRefresh_FreshSessionWithNilLastUsedFallsBackToCreatedAt(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	sess := &sessionstore.Session{
		InternalID: 1,
		PublicID:   dbtype.New(),
		UserID:     7,
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour),
		LastUsedAt: nil,
		CreatedAt:  time.Now().Add(-time.Minute),
	}
	deps := Deps{
		JWT:      jwt,
		Sessions: &stubRefreshSessions{session: sess},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("fresh session must pass the idle-timeout gate via CreatedAt fallback")
		}
	}()
	_, _ = Refresh(deps)(context.Background(), &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "anything"},
	})
}

// TestRefresh_FreshSessionWithStaleCreatedAtIsRejected guards against
// the symmetric mistake: if a session was created long ago but never
// used (LastUsedAt nil), the fallback to CreatedAt must still trip the
// idle gate. Otherwise an attacker could keep a long-dormant cookie
// alive by avoiding refresh until just before the wall-clock expiry.
func TestRefresh_FreshSessionWithStaleCreatedAtIsRejected(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	sess := &sessionstore.Session{
		InternalID: 1,
		PublicID:   dbtype.New(),
		UserID:     7,
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour),
		LastUsedAt: nil,
		CreatedAt:  time.Now().Add(-(sessionIdleTimeout + 24*time.Hour)),
	}
	deps := Deps{
		JWT:      jwt,
		Sessions: &stubRefreshSessions{session: sess},
	}

	_, err = Refresh(deps)(context.Background(), &RefreshInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "anything"},
	})
	require.Error(t, err)
	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	assert.Equal(t, apierrors.AuthTokenRefreshExpired.Code, problem.Type)
}

// Compile-time guard: stubRefreshSessions must satisfy
// sessionstore.Store even though we only exercise a subset.
var _ sessionstore.Store = (*stubRefreshSessions)(nil)

package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// fakeSessions is a minimal in-memory [sessionstore.Store] for tests
// of the logout handler. Only the methods Logout actually exercises
// are implemented; the rest panic so unrelated drift is caught.
type fakeSessions struct {
	findHash    string
	findSession *sessionstore.Session
	findErr     error

	revokeErr     error
	revokeCalls   []revokeCall
	revokeAttempt int
}

type revokeCall struct {
	userID   uint32
	publicID dbtype.PublicID
}

func (f *fakeSessions) Create(_ context.Context, _ sessionstore.CreateParams) (uint32, error) {
	panic("not implemented")
}

func (f *fakeSessions) FindByRefreshHash(_ context.Context, hash string) (*sessionstore.Session, error) {
	f.findHash = hash
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.findSession, nil
}

func (f *fakeSessions) RotateRefreshHash(_ context.Context, _, _ string, _ time.Time) error {
	panic("not implemented")
}

func (f *fakeSessions) Revoke(_ context.Context, userID uint32, publicID dbtype.PublicID) error {
	f.revokeAttempt++
	f.revokeCalls = append(f.revokeCalls, revokeCall{userID: userID, publicID: publicID})
	return f.revokeErr
}

func (f *fakeSessions) ListActive(_ context.Context, _ uint32) ([]sessionstore.Session, error) {
	panic("not implemented")
}

func (f *fakeSessions) RevokeAllExcept(_ context.Context, _ uint32, _ dbtype.PublicID) error {
	panic("not implemented")
}

func (f *fakeSessions) FindAnyByRefreshHash(_ context.Context, _ string) (*sessionstore.Session, error) {
	panic("not implemented")
}

func (f *fakeSessions) RevokeAllForUser(_ context.Context, _ uint32) error {
	panic("not implemented")
}

// TestLogout_SuccessfulRevokeWritesAuditEntry asserts that a logout
// hitting an active session both revokes the session and emits a
// canonical "auth.logout" audit entry against the actor. The audit
// metadata must carry the session public id so investigators can
// correlate the logout with the matching session row in the audit
// feed.
func TestLogout_SuccessfulRevokeWritesAuditEntry(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	sessionPublic := dbtype.New()
	sessions := &fakeSessions{
		findSession: &sessionstore.Session{
			InternalID: 99,
			PublicID:   sessionPublic,
			UserID:     7,
		},
	}
	deps := Deps{
		Sessions: sessions,
		Audit:    sink,
	}

	plain := "refresh-token-plaintext"
	in := &LogoutInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: plain}, //#nosec G124 -- test cookie
	}
	out, err := Logout(deps)(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.True(t, out.Body.Ok, "logout must report success even when there is more to assert")

	// FindByRefreshHash must have been called with the SHA-256 hex of
	// the cookie value, never with the plaintext.
	assert.Equal(t, auth.HashOpaque(plain), sessions.findHash,
		"handler must hash the refresh cookie before lookup")
	require.Len(t, sessions.revokeCalls, 1, "exactly one Revoke call expected")
	assert.Equal(t, uint32(7), sessions.revokeCalls[0].userID)
	assert.Equal(t, sessionPublic, sessions.revokeCalls[0].publicID)

	entries := sink.snapshot()
	require.Len(t, entries, 1, "exactly one audit entry expected")
	got := entries[0]
	assert.Equal(t, "auth.logout", got.Action,
		"audit action must be the canonical 'auth.logout' identifier")
	assert.Equal(t, uint32(7), got.ActorID,
		"actor must be the session's user, not the cookie value")
	assert.Equal(t, "session", got.ResourceType)
	assert.Equal(t, sessionPublic.String(), got.ResourceID,
		"resource id must point at the session that was revoked")
	require.NotNil(t, got.Metadata)
	assert.Equal(t, sessionPublic.String(), got.Metadata["session_id"])
}

// TestLogout_NoCookieIsIdempotent asserts the handler still returns a
// cleared cookie and ok=true when the request carries no refresh
// cookie, and that NO audit entry is written for that no-op (an
// audit row would be pure noise — there was nothing to revoke).
func TestLogout_NoCookieIsIdempotent(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	sessions := &fakeSessions{}
	deps := Deps{Sessions: sessions, Audit: sink}

	out, err := Logout(deps)(context.Background(), &LogoutInput{})
	require.NoError(t, err)
	assert.True(t, out.Body.Ok)
	assert.Empty(t, sessions.revokeCalls, "no Revoke should fire without a cookie")
	assert.Empty(t, sink.snapshot(), "no audit entry should fire without a session")
}

// TestLogout_RevokeFailureIsLoggedNotReturned asserts that when the
// session store fails to revoke, the handler still returns ok (so
// the client clears its cookie) and does NOT write a logout audit
// entry — the audit row would be a lie if the revoke did not stick.
// The error must propagate via slog instead of being silently
// dropped via `_ =`. We cannot easily intercept slog from here; the
// regression we are guarding is the behavioural one: ok=true,
// revoke attempted exactly once, no audit emitted.
func TestLogout_RevokeFailureIsLoggedNotReturned(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	sessions := &fakeSessions{
		findSession: &sessionstore.Session{
			InternalID: 1,
			PublicID:   dbtype.New(),
			UserID:     5,
		},
		revokeErr: errors.New("simulated store failure"),
	}
	deps := Deps{Sessions: sessions, Audit: sink}

	out, err := Logout(deps)(context.Background(), &LogoutInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "x"}, //#nosec G124 -- test cookie
	})
	require.NoError(t, err, "logout must remain idempotent even on revoke failure")
	assert.True(t, out.Body.Ok)
	assert.Equal(t, 1, sessions.revokeAttempt, "revoke must be attempted exactly once")
	assert.Empty(t, sink.snapshot(),
		"audit entry must NOT be emitted when the revoke failed; "+
			"the entry would falsely claim the session was killed")
}

// TestLogout_FindFailureIsIdempotent asserts the handler degrades to
// ok+cleared-cookie when FindByRefreshHash returns an error (e.g.
// the cookie has already been rotated away by another tab). No
// Revoke and no audit entry should fire.
func TestLogout_FindFailureIsIdempotent(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	sessions := &fakeSessions{
		findErr: sessionstore.ErrNotFound,
	}
	deps := Deps{Sessions: sessions, Audit: sink}

	out, err := Logout(deps)(context.Background(), &LogoutInput{
		RefreshCookie: http.Cookie{Name: "nd_rt", Value: "stale"}, //#nosec G124 -- test cookie
	})
	require.NoError(t, err)
	assert.True(t, out.Body.Ok)
	assert.Empty(t, sessions.revokeCalls)
	assert.Empty(t, sink.snapshot())
}

// TestRecordLogoutAudit_NilSinkIsSafe asserts the helper does not
// panic when audit is unconfigured, mirroring the contract of the
// other recordXxxAudit helpers in this package.
func TestRecordLogoutAudit_NilSinkIsSafe(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		recordLogoutAudit(context.Background(), Deps{Audit: nil}, 1, "01961234-5678-7000-8000-aaaaaaaaaaaa")
	})
}

// TestRecordLogoutAudit_EmitsExpectedEntry asserts the helper produces
// the canonical action/actor/resource shape and embeds the session
// public id in metadata so investigators can correlate.
func TestRecordLogoutAudit_EmitsExpectedEntry(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	sessionID := "01961234-5678-7000-8000-aaaaaaaaaaaa"
	recordLogoutAudit(context.Background(), Deps{Audit: sink}, 11, sessionID)

	entries := sink.snapshot()
	require.Len(t, entries, 1)
	got := entries[0]
	assert.Equal(t, "auth.logout", got.Action)
	assert.Equal(t, uint32(11), got.ActorID)
	assert.Equal(t, "session", got.ResourceType)
	assert.Equal(t, sessionID, got.ResourceID)
	assert.Equal(t, sessionID, got.Metadata["session_id"])
}

// Compile-time guard: fakeSessions must satisfy sessionstore.Store
// even though we only exercise a subset.
var _ sessionstore.Store = (*fakeSessions)(nil)

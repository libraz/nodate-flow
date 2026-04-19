package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// TestInviteTokenHash_StoresHashNotPlaintext verifies that when a
// calendar invite is created, the database stores a SHA-256 hash of the
// token rather than the plaintext token itself.
func TestInviteTokenHash_StoresHashNotPlaintext(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	// Create an invite via the API and capture the plaintext token.
	var resp struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "invites"),
		tt.AccessToken, map[string]any{"role": "viewer"}, &resp)
	require.NotEmpty(t, resp.Token, "invite API must return a plaintext token")
	require.NotEmpty(t, resp.ID, "invite API must return an id")

	// Query the database directly to verify the stored value is a hash.
	q := generated.New(testDB)
	expectedHash := authn.HashOpaque(resp.Token)
	row, err := q.FindCalendarInviteByTokenHash(context.Background(), expectedHash)
	require.NoError(t, err, "looking up invite by hash of returned token must succeed")
	assert.Equal(t, resp.ID, row.PublicID.String(),
		"the invite found by hash must match the one created")

	// The stored token_hash must equal SHA-256(plaintext), not the
	// plaintext itself.
	assert.Equal(t, expectedHash, row.TokenHash,
		"stored token_hash must be SHA-256 of plaintext token")
	assert.NotEqual(t, resp.Token, row.TokenHash,
		"stored token_hash must NOT be the plaintext token")
}

// TestInviteTokenHash_LookupByPlaintextFails verifies that looking up
// an invite using the raw plaintext token (instead of its hash) returns
// no rows.
func TestInviteTokenHash_LookupByPlaintextFails(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var resp struct {
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "invites"),
		tt.AccessToken, map[string]any{"role": "editor"}, &resp)
	require.NotEmpty(t, resp.Token)

	// Attempting to find the invite by plaintext must fail.
	q := generated.New(testDB)
	_, err := q.FindCalendarInviteByTokenHash(context.Background(), resp.Token)
	require.Error(t, err, "looking up invite by plaintext token must fail")
}

// TestInviteTokenHash_AcceptUsesHash verifies the accept endpoint
// correctly hashes the supplied plaintext token internally so that an
// invite can be accepted via the token returned at creation time.
func TestInviteTokenHash_AcceptUsesHash(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	// Owner creates an invite.
	var inv struct {
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "invites"),
		owner.AccessToken, map[string]any{"role": "viewer"}, &inv)
	require.NotEmpty(t, inv.Token)

	// A different user accepts the invite using the plaintext token.
	acceptor := newTenant(t)
	var acceptResp struct {
		CalendarID string `json:"calendarId"`
		Role       string `json:"role"`
	}
	helpers.DoJSON(t, http.MethodPost,
		testSrv.BaseURL+"/invites/"+inv.Token+"/accept",
		acceptor.AccessToken, nil, &acceptResp)

	assert.Equal(t, calID, acceptResp.CalendarID,
		"accepted invite must reference the correct calendar")
	assert.Equal(t, "viewer", acceptResp.Role,
		"accepted invite must grant the specified role")
}

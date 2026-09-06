package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

const expiresAtNotInFutureCode = "SHARE.SHARE.EXPIRES_AT_NOT_IN_FUTURE"

// expirePublicShare stamps a share's expires_at directly. The write path
// refuses an expiry that is not in the future, so this is the only way to
// reach the state of a share that has since run out — which the render
// path and the patch path both still have to handle.
func expirePublicShare(t *testing.T, sharePublicID string, at time.Time) {
	t.Helper()
	res, err := testDB.ExecContext(
		context.Background(),
		`UPDATE calendar_public_shares SET expires_at = ? WHERE public_id = UUID_TO_BIN(?, 0)`,
		at.UTC(), sharePublicID,
	)
	require.NoError(t, err, "stamp expires_at on share %s", sharePublicID)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "share %s must exist to be expired", sharePublicID)
}

// requireExpiresAtNotInFuture asserts the response is the machine-readable
// refusal of a past expiry, not merely some failure.
func requireExpiresAtNotInFuture(t *testing.T, status int, body []byte) {
	t.Helper()
	var envelope struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "decode envelope body=%s", string(body))
	assert.Equal(t, expiresAtNotInFutureCode, envelope.Type, "body=%s", string(body))
	assert.Equal(t, http.StatusUnprocessableEntity, envelope.Status, "body=%s", string(body))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
}

func readShareExpiresAt(t *testing.T, tt *helpers.CalendarTestTenant, shareID string) *int64 {
	t.Helper()
	var got struct {
		Share struct {
			ExpiresAt *int64 `json:"expiresAt"`
		} `json:"share"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("public-shares", shareID), tt.AccessToken, nil, &got)
	return got.Share.ExpiresAt
}

// TestCreatePublicShare_RejectsPastExpiry covers the create path: an
// expiry that is not later than the moment the request is handled would
// mint a share that is already expired in its own response.
func TestCreatePublicShare_RejectsPastExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := helpers.DoJSONStatus(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title":     "Born Expired",
		"expiresAt": time.Now().Add(-1 * time.Hour).Unix(),
	})
	requireExpiresAtNotInFuture(t, status, body)

	var list struct {
		Shares []struct {
			ID string `json:"id"`
		} `json:"shares"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("public-shares"), tt.AccessToken, nil, &list)
	assert.Empty(t, list.Shares, "a refused create must not leave a share behind")
}

// TestCreatePublicShare_RejectsExpiryEqualToNow pins the boundary: an
// expiry landing in the current second has elapsed by the time the row
// is written, so equality is refused along with the past.
func TestCreatePublicShare_RejectsExpiryEqualToNow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := helpers.DoJSONStatus(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title":     "Expires Now",
		"expiresAt": time.Now().Unix(),
	})
	requireExpiresAtNotInFuture(t, status, body)
}

func TestCreatePublicShare_AcceptsFutureExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	future := time.Now().Add(1 * time.Hour).Unix()

	var created struct {
		ID        string `json:"id"`
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title":     "Expires Later",
		"expiresAt": future,
	}, &created)
	require.NotNil(t, created.ExpiresAt, "a future expiry must be stored")
	assert.Equal(t, future, *created.ExpiresAt)
}

// TestCreatePublicShare_AcceptsOmittedExpiry keeps "omitted means no
// expiry" from being turned into a validation failure.
func TestCreatePublicShare_AcceptsOmittedExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID        string `json:"id"`
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Never Expires",
	}, &created)
	require.NotEmpty(t, created.ID)
	assert.Nil(t, created.ExpiresAt, "omitting expiresAt must mean no expiry")
	assert.Nil(t, readShareExpiresAt(t, tt, created.ID))
}

func TestPatchPublicShare_RejectsPastExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Patch To Past")

	status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": time.Now().Add(-1 * time.Hour).Unix(),
	})
	requireExpiresAtNotInFuture(t, status, body)
	assert.Nil(t, readShareExpiresAt(t, tt, shareID), "a refused patch must not stamp an expiry")
}

func TestPatchPublicShare_RejectsExpiryEqualToNow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Patch To Now")

	status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": time.Now().Unix(),
	})
	requireExpiresAtNotInFuture(t, status, body)
	assert.Nil(t, readShareExpiresAt(t, tt, shareID))
}

func TestPatchPublicShare_AcceptsFutureExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Patch To Future")
	future := time.Now().Add(2 * time.Hour).Unix()

	var patched struct {
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": future,
	}, &patched)
	require.NotNil(t, patched.ExpiresAt)
	assert.Equal(t, future, *patched.ExpiresAt)
}

// TestPatchPublicShare_AcceptsOmittedExpiry patches another field and
// leaves the stored expiry alone; an absent field is not a supplied
// value and must not be validated.
func TestPatchPublicShare_AcceptsOmittedExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Keep Expiry")
	future := time.Now().Add(3 * time.Hour).Unix()
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": future,
	}, nil)

	var patched struct {
		Title     string `json:"title"`
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"title": "Renamed",
	}, &patched)
	assert.Equal(t, "Renamed", patched.Title)
	require.NotNil(t, patched.ExpiresAt)
	assert.Equal(t, future, *patched.ExpiresAt, "omitting expiresAt must leave the stored value alone")
}

// TestPatchPublicShare_AcceptsClearExpiresAt keeps the clear path open:
// dropping the expiry supplies no timestamp to validate.
func TestPatchPublicShare_AcceptsClearExpiresAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Clear Expiry")
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": time.Now().Add(4 * time.Hour).Unix(),
	}, nil)
	require.NotNil(t, readShareExpiresAt(t, tt, shareID))

	var cleared struct {
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"clearExpiresAt": true,
	}, &cleared)
	assert.Nil(t, cleared.ExpiresAt, "clearExpiresAt must drop the expiry")
	assert.Nil(t, readShareExpiresAt(t, tt, shareID))
}

// TestPatchPublicShare_ClearExpiresAtWinsOverPastExpiry pins the
// precedence between the two fields. The clear is applied after the
// field update, so the share ends with no expiry — there is no value
// left to be in the past, and refusing the request would block a caller
// from clearing a stale expiry.
func TestPatchPublicShare_ClearExpiresAtWinsOverPastExpiry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Clear Beats Past")

	var patched struct {
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt":      time.Now().Add(-1 * time.Hour).Unix(),
		"clearExpiresAt": true,
	}, &patched)
	assert.Nil(t, patched.ExpiresAt, "clearExpiresAt wins, so the share ends with no expiry")
	assert.Nil(t, readShareExpiresAt(t, tt, shareID))
}

// TestPatchPublicShare_AlreadyExpiredShareAcceptsOtherFields covers the
// share that outlived an expiry which was valid when it was set. The
// caller renaming it supplies no expiry, so the refusal does not apply
// and the stale timestamp is carried through untouched.
func TestPatchPublicShare_AlreadyExpiredShareAcceptsOtherFields(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	shareID, _ := createShare(t, tt, "Long Expired")
	past := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	expirePublicShare(t, shareID, past)

	var patched struct {
		Title     string `json:"title"`
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"title": "Long Expired, Renamed",
	}, &patched)
	assert.Equal(t, "Long Expired, Renamed", patched.Title)
	require.NotNil(t, patched.ExpiresAt, "the stale expiry must survive a patch that does not touch it")
	assert.Equal(t, past.Unix(), *patched.ExpiresAt)

	// The same share can still be given a valid expiry back.
	future := time.Now().Add(5 * time.Hour).Unix()
	var revived struct {
		ExpiresAt *int64 `json:"expiresAt"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", shareID), tt.AccessToken, map[string]any{
		"expiresAt": future,
	}, &revived)
	require.NotNil(t, revived.ExpiresAt)
	assert.Equal(t, future, *revived.ExpiresAt)
}

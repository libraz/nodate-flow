package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// beyondDatetimeUnix is 10000-01-01T00:00:00Z, one second past the last
// instant a MySQL DATETIME can hold.
const beyondDatetimeUnix int64 = 253402300800

// TestShareExpiryBeyondDatetimeIsRefused holds create and patch to the
// catalog's out-of-range refusal for an expiry the column cannot store,
// instead of a retryable store failure, and to writing nothing.
func TestShareExpiryBeyondDatetimeIsRefused(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/public-shares"

	status, raw := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"title":     "Far future",
		"expiresAt": beyondDatetimeUnix,
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "SHARE.SHARE.EXPIRES_AT_OUT_OF_RANGE", problemType(t, raw))

	var list struct {
		Shares []struct {
			ID string `json:"id"`
		} `json:"shares"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &list)
	require.Empty(t, list.Shares, "a refused create must not leave a share behind")

	var share struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{"title": "Patch me"}, &share)
	require.NotEmpty(t, share.ID)

	status, raw = doJSONStatus(t, http.MethodPatch, base+"/"+share.ID, tt.AccessToken, map[string]any{
		"expiresAt": beyondDatetimeUnix,
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "SHARE.SHARE.EXPIRES_AT_OUT_OF_RANGE", problemType(t, raw))

	var got struct {
		Share struct {
			ExpiresAt *int64 `json:"expiresAt"`
		} `json:"share"`
	}
	doJSON(t, http.MethodGet, base+"/"+share.ID, tt.AccessToken, nil, &got)
	require.Nil(t, got.Share.ExpiresAt, "a refused patch must not stamp an expiry")
}

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLensSharedAtSurvivesRepublish holds that sharedAt stays the first
// publication time when a lens is revoked and published again.
func TestLensSharedAtSurvivesRepublish(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	wsBase := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	var lens struct {
		ID       string `json:"id"`
		SharedAt *int64 `json:"sharedAt"`
	}
	doJSON(t, http.MethodPost, wsBase+"/lenses", tt.AccessToken, map[string]any{
		"name":      "Shared " + randomHex(4),
		"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID)

	doJSON(t, http.MethodPost, wsBase+"/lenses/"+lens.ID+"/publish", tt.AccessToken, nil, nil)

	// Back-date the first share so a second publish that restamps it is visible.
	first := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	_, err := testDB.ExecContext(context.Background(),
		`UPDATE lenses SET shared_at = ? WHERE public_id = UUID_TO_BIN(?, 0)`, first, lens.ID)
	require.NoError(t, err)

	doJSON(t, http.MethodPost, wsBase+"/lenses/"+lens.ID+"/unpublish", tt.AccessToken, nil, nil)
	doJSON(t, http.MethodPost, wsBase+"/lenses/"+lens.ID+"/publish", tt.AccessToken, nil, nil)

	var got struct {
		SharedAt *int64 `json:"sharedAt"`
	}
	doJSON(t, http.MethodGet, wsBase+"/lenses/"+lens.ID, tt.AccessToken, nil, &got)
	require.NotNil(t, got.SharedAt)
	require.Equal(t, first.Unix(), *got.SharedAt, "republishing must keep the first share time")
}

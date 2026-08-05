// Lens share-token hygiene. A public lens token is a bearer credential
// for an unauthenticated URL, so the invariant is narrow and absolute:
// the plaintext exists in exactly one response body, at publish time,
// and nowhere else. Not in the event log, which is append-only and
// readable by every workspace member — including members who joined
// after the publish and members who have since been removed from the
// projects the lens covers. Not in the audit log, which outlives the
// share by design. Not in the lens read endpoints.
//
// The assertions search whole response bodies and whole stored JSON
// columns rather than named fields, because the failure mode this
// guards against is a token reaching a field nobody thought to check.
package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// publishLensForHygiene creates a lens, publishes it, and returns the
// lens public id plus the plaintext token handed back exactly once.
func publishLensForHygiene(t *testing.T, base, accessToken, name string) (lensID, token string) {
	t.Helper()

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, accessToken, map[string]any{
		"name":      name,
		"filter":    json.RawMessage(`{}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID)

	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish", accessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken, "publish must return the plaintext token")
	return created.ID, published.PublicToken
}

// TestLensPublishTokenNeverReachesTimelineOrAudit is the C-3 regression.
// After a publish, the token must not be recoverable from any record
// the workspace keeps: the timeline endpoint, the events table behind
// it, the audit log, or the lens read endpoints.
func TestLensPublishTokenNeverReachesTimelineOrAudit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	lensID, token := publishLensForHygiene(t, base, tt.AccessToken, "Hygiene Board")

	// The workspace timeline passes event payloads through unmodified,
	// so it is the surface where a payload-borne token would surface
	// first. Search the raw body: a token in a field this test does not
	// know about is still a token on the wire.
	status, timelineBody := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeline?limit=200",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "timeline must render; body=%s", string(timelineBody))
	assert.NotContains(t, string(timelineBody), token,
		"the share token must not appear anywhere in the workspace timeline")

	// The lens.shared event must still be recorded — the fix removes the
	// credential from the payload, not the record of the state change.
	assert.Contains(t, string(timelineBody), lensID,
		"the publish must still be recorded on the timeline, keyed by lens id")

	// The events table is the durable copy behind the timeline. Reading
	// it directly closes the gap where a payload field is stored but
	// happens to be filtered out of the current response shape.
	var payloads []string
	rows, err := testDB.Query(
		`SELECT CAST(payload_json AS CHAR)
		   FROM events
		  WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var p string
		require.NoError(t, rows.Scan(&p))
		payloads = append(payloads, p)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, payloads, "the workspace must have recorded events")
	for _, p := range payloads {
		assert.NotContains(t, p, token, "no event payload may carry the share token")
	}

	// Audit rows outlive the share and are read by admins, so the
	// metadata column has to be clean too.
	var auditMeta []string
	auditRows, err := testDB.Query(
		`SELECT COALESCE(CAST(metadata_json AS CHAR), '')
		   FROM audit_logs
		  WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID)
	require.NoError(t, err)
	defer func() { _ = auditRows.Close() }()
	for auditRows.Next() {
		var m string
		require.NoError(t, auditRows.Scan(&m))
		auditMeta = append(auditMeta, m)
	}
	require.NoError(t, auditRows.Err())
	for _, m := range auditMeta {
		assert.NotContains(t, m, token, "no audit metadata may carry the share token")
	}

	// The lens read endpoints must not hand the token back either — the
	// row does not hold it any more, and a field that reappears would
	// mean the plaintext went back into the database.
	status, listBody := doJSONStatus(t, http.MethodGet, base, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "lens list must render; body=%s", string(listBody))
	assert.NotContains(t, string(listBody), token,
		"the lens list must not re-expose the share token")
	assert.Contains(t, string(listBody), `"isPublic":true`,
		"the lens list must still report that the lens is published")

	status, getBody := doJSONStatus(t, http.MethodGet, base+"/"+lensID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "lens get must render; body=%s", string(getBody))
	assert.NotContains(t, string(getBody), token,
		"the lens get must not re-expose the share token")
}

// TestLensPublicTokenStoredAsHash pins the storage shape. The column
// must hold the SHA-256 of the token and never the token itself, which
// is what makes the leak paths above unrecoverable rather than merely
// unexposed.
func TestLensPublicTokenStoredAsHash(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	lensID, token := publishLensForHygiene(t, base, tt.AccessToken, "Hash Board")

	var stored string
	require.NoError(t, testDB.QueryRow(
		`SELECT public_token_hash FROM lenses WHERE public_id = UUID_TO_BIN(?, 0)`,
		lensID).Scan(&stored))
	assert.Equal(t, sharedtoken.HashToken(token), stored,
		"the column must hold the SHA-256 hex of the token")
	assert.NotEqual(t, token, stored, "the column must not hold the plaintext")
	assert.Len(t, stored, 64, "SHA-256 hex is 64 characters")
	assert.Equal(t, strings.ToLower(stored), stored, "the hash must be lowercase hex")
}

// TestLensPublicAccessSurvivesHashing verifies the other half: hashing
// at rest must not break the capability. The token still opens the page,
// a near-miss token does not, and unpublishing revokes it.
func TestLensPublicAccessSurvivesHashing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	lensID, token := publishLensForHygiene(t, base, tt.AccessToken, "Access Board")

	// The minted token resolves through the hash comparison.
	var pub struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+token, "", nil, &pub)
	assert.Equal(t, lensID, pub.ID)
	assert.Equal(t, "Access Board", pub.Name)
	assert.NotNil(t, pub.Tasks, "tasks must be present even when empty")

	// The stored hash is not itself a usable token: presenting it must
	// not open the page. This is what distinguishes hashing at rest from
	// renaming the column.
	var stored string
	require.NoError(t, testDB.QueryRow(
		`SELECT public_token_hash FROM lenses WHERE public_id = UUID_TO_BIN(?, 0)`,
		lensID).Scan(&stored))
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/public/lenses/"+stored, "", nil)
	assert.Equal(t, http.StatusNotFound, status,
		"the stored hash must not work as a share token")

	// Unpublishing clears the hash, so the URL stops resolving.
	doJSON(t, http.MethodPost, base+"/"+lensID+"/unpublish", tt.AccessToken, nil, nil)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/public/lenses/"+token, "", nil)
	assert.Equal(t, http.StatusNotFound, status,
		"an unpublished lens must not be reachable by its old token; body=%s", string(body))

	var afterHash any
	require.NoError(t, testDB.QueryRow(
		`SELECT public_token_hash FROM lenses WHERE public_id = UUID_TO_BIN(?, 0)`,
		lensID).Scan(&afterHash))
	assert.Nil(t, afterHash, "unpublish must clear the stored hash")
}

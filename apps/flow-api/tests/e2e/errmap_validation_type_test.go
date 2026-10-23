package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaRefusalCarriesACatalogCode holds a refusal Huma raises before
// the handler runs to a type the catalog defines. An overlong title used
// to come back with no type at all, leaving a client nothing to branch on.
func TestSchemaRefusalCarriesACatalogCode(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     strings.Repeat("a", 501),
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))
}

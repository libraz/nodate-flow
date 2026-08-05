package e2e

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAuditListRendersIPAddressAsText proves the audit read path undoes
// the VARBINARY(16) packing applied on write. The column holds packed
// bytes, so a mapper that passes it through unchanged shows the operator
// investigating an incident an unreadable blob in the one column that
// matters most for forensics.
func TestAuditListRendersIPAddressAsText(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?limit=50&offset=0", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)
	require.NotEmpty(t, resp.Entries, "tenant setup must have produced audit rows")

	withIP := 0
	for _, e := range resp.Entries {
		if e.IPAddress == nil {
			continue
		}
		withIP++
		addr, err := netip.ParseAddr(*e.IPAddress)
		require.NoErrorf(t, err,
			"%s: ipAddress %q is not a readable address; the packed column "+
				"is reaching the API unconverted", e.Action, *e.IPAddress)
		require.Truef(t, addr.IsLoopback(),
			"%s: ipAddress = %q, want the loopback address the test client connects from",
			e.Action, addr)
	}
	require.NotZero(t, withIP, "at least one audit row must carry the caller's IP")
}

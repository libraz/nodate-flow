package audit

import (
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// TestMapListRowRendersIPAddressAsText pins the read half of the packed
// ip_address column: audit_logs.ip_address is VARBINARY(16), so a mapper
// that copies the column straight onto the DTO hands the operator
// investigating an incident a mangled binary blob instead of the address.
func TestMapListRowRendersIPAddressAsText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
	}{
		{name: "ipv4", want: "203.0.113.7"},
		{name: "ipv6", want: "2001:db8:85a3:8d3:1319:8a2e:370:7348"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dto := mapListRow(generated.ListWorkspaceAuditLogsRow{
				Action:    "auth.login_failed",
				IpAddress: dbtype.NullStringFromIP(tc.want),
			})
			if dto.IPAddress == nil {
				t.Fatalf("ipAddress is nil, want %q", tc.want)
			}
			if *dto.IPAddress != tc.want {
				t.Fatalf("ipAddress = %q, want %q", *dto.IPAddress, tc.want)
			}
		})
	}
}

// TestMapListRowOmitsMissingIPAddress keeps SQL NULL distinguishable
// from an address on the wire: the field is "string | null" and a row
// recorded without a client IP must serialise as null, not "".
func TestMapListRowOmitsMissingIPAddress(t *testing.T) {
	t.Parallel()

	for _, stored := range []sql.NullString{{}, {String: "", Valid: true}} {
		dto := mapListRow(generated.ListWorkspaceAuditLogsRow{
			Action:    "auth.login",
			IpAddress: stored,
		})
		if dto.IPAddress != nil {
			t.Fatalf("ipAddress = %q, want nil for %+v", *dto.IPAddress, stored)
		}
	}
}

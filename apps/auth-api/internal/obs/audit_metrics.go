package obs

import (
	"github.com/prometheus/client_golang/prometheus"

	sharedobs "github.com/libraz/nodate-flow/packages/go-shared/obs"
)

// Audit destination table labels.
const (
	// AuditTableWorkspace labels a failed workspace-scoped audit write.
	AuditTableWorkspace = sharedobs.AuditTableWorkspace
	// AuditTableInstance labels a failed instance-scoped audit write.
	AuditTableInstance = sharedobs.AuditTableInstance
)

// IncAuditWriteFailure increments the audit-loss counter for the given
// destination table. Pass one of the AuditTable* constants.
//
// The counter is nf_audit_write_failures_total, declared once in
// packages/go-shared/obs because every service exports it under that
// name. This service owns identity, so the actions that go missing here
// are logins, session revocations, and role changes: any sustained value
// means the identity audit trail is incomplete for that window.
func IncAuditWriteFailure(table string) {
	sharedobs.IncAuditWriteFailure(table)
}

// AuditWriteFailuresCounter returns the prometheus counter for the given
// table label. Exposed so tests in adjacent packages can call
// testutil.ToFloat64 on a specific label combination without taking a
// dependency on the metric vector.
func AuditWriteFailuresCounter(table string) prometheus.Counter {
	return sharedobs.AuditWriteFailuresCounter(table)
}

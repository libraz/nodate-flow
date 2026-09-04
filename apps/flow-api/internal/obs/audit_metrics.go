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
// name. A sustained value here means this service answered 2xx for
// actions that left no trace; correlate with
// nf_db_queries_total{status="error"} to confirm the DB is the cause.
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

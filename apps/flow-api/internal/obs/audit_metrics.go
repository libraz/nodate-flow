package obs

import (
	"github.com/prometheus/client_golang/prometheus"
)

// auditWriteFailuresTotal counts audit rows the recorder built but could
// not store. Partitioned by the destination table so a workspace-scoped
// loss (audit_logs) is separable from an instance-scoped one
// (instance_audit_logs).
//
// An audit write never fails the request that triggered it, so a
// non-zero rate is the only sign that the API answered 2xx for an
// action that left no trace. Any sustained value here means the audit
// trail is incomplete for that window; correlate with
// nf_db_queries_total{status="error"} to confirm the DB is the cause.
var auditWriteFailuresTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_audit_write_failures_total",
		Help: "Total audit rows that could not be stored, partitioned by destination table.",
	},
	[]string{"table"},
)

func init() {
	prometheus.MustRegister(auditWriteFailuresTotal)
}

// Audit destination table labels.
const (
	// AuditTableWorkspace labels a failed workspace-scoped audit write.
	AuditTableWorkspace = "audit_logs"
	// AuditTableInstance labels a failed instance-scoped audit write.
	AuditTableInstance = "instance_audit_logs"
)

// IncAuditWriteFailure increments the audit-loss counter for the given
// destination table. Pass one of the AuditTable* constants.
func IncAuditWriteFailure(table string) {
	auditWriteFailuresTotal.WithLabelValues(table).Inc()
}

// AuditWriteFailuresCounter returns the prometheus counter for the given
// table label. Exposed so tests in adjacent packages can call
// testutil.ToFloat64 on a specific label combination without taking a
// dependency on the unexported metric vector.
func AuditWriteFailuresCounter(table string) prometheus.Counter {
	return auditWriteFailuresTotal.WithLabelValues(table)
}

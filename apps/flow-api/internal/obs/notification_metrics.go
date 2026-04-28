package obs

import (
	"context"
	stderrors "errors"

	"github.com/prometheus/client_golang/prometheus"
)

// notificationFanoutPrefFetchErrorsTotal counts failures when the
// fan-out path tries to load the (recipient, channel) preference
// rows. Partitioned by error class so dashboards can separate "DB
// is sad" (type=db) from "we ran out of budget" (type=timeout).
//
// A non-zero rate means recipients are silently falling through to
// the in_app-only default; correlate with nf_db_queries_total{status="error"}
// for the same time range to confirm the DB is the underlying cause.
var notificationFanoutPrefFetchErrorsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_notification_fanout_preference_fetch_errors_total",
		Help: "Total notification preference fetch failures during fan-out, partitioned by error class (db|timeout).",
	},
	[]string{"type"},
)

// notificationFanoutDedupTotal counts notification rows that hit the
// (recipient, source_event, channel) UNIQUE key and were deduplicated
// by the INSERT IGNORE. Reasons:
//   - "unique_collision" — the canonical at-least-once happy path
//     (the same hook fired twice for the same event).
//
// A persistent non-zero rate is normal under at-least-once delivery
// but a sudden spike usually means a hook is fan-firing more than
// expected (e.g. eventbus replay misconfiguration).
var notificationFanoutDedupTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_notification_fanout_dedup_total",
		Help: "Total notification rows deduplicated during fan-out, partitioned by reason.",
	},
	[]string{"reason"},
)

func init() {
	prometheus.MustRegister(notificationFanoutPrefFetchErrorsTotal)
	prometheus.MustRegister(notificationFanoutDedupTotal)
}

// IncNotificationFanoutPreferenceFetchError increments the
// preference-fetch error counter. err is inspected to derive the
// "type" label: a context.DeadlineExceeded surfaces as "timeout",
// any other error as "db". Pass a non-nil error.
func IncNotificationFanoutPreferenceFetchError(err error) {
	label := "db"
	if stderrors.Is(err, context.DeadlineExceeded) || stderrors.Is(err, context.Canceled) {
		label = "timeout"
	}
	notificationFanoutPrefFetchErrorsTotal.WithLabelValues(label).Inc()
}

// IncNotificationFanoutDedup increments the dedup counter for the
// given reason label.
func IncNotificationFanoutDedup(reason string) {
	notificationFanoutDedupTotal.WithLabelValues(reason).Inc()
}

// NotificationFanoutPreferenceFetchErrorsCounter returns the prometheus
// counter for the (type) label combination. Exposed so tests in adjacent
// packages can call testutil.ToFloat64 on specific label combinations
// without taking a dependency on the unexported metric vector.
func NotificationFanoutPreferenceFetchErrorsCounter(typ string) prometheus.Counter {
	return notificationFanoutPrefFetchErrorsTotal.WithLabelValues(typ)
}

// NotificationFanoutDedupCounter returns the prometheus counter for
// the (reason) label combination. See
// [NotificationFanoutPreferenceFetchErrorsCounter] for the rationale.
func NotificationFanoutDedupCounter(reason string) prometheus.Counter {
	return notificationFanoutDedupTotal.WithLabelValues(reason)
}

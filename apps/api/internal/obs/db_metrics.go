package obs

import (
	"database/sql"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// dbQueryDuration observes query latency in seconds partitioned by operation
// name (e.g. "ListTasks", "CreateTask"). The default histogram buckets cover
// the range from 5 ms to 10 s which is suitable for most database workloads.
var dbQueryDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "nf_db_query_duration_seconds",
		Help:    "Histogram of database query latencies in seconds, partitioned by query name.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"query"},
)

// dbQueriesTotal counts completed database queries partitioned by operation
// name and outcome status ("ok" or "error").
var dbQueriesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_db_queries_total",
		Help: "Total number of database queries completed, partitioned by query name and status.",
	},
	[]string{"query", "status"},
)

// dbConnectionsOpen tracks the number of open connections reported by
// sql.DBStats.OpenConnections. Updated by StartDBStatsCollector.
var dbConnectionsOpen = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "nf_db_connections_open",
		Help: "Number of open database connections (from sql.DBStats.OpenConnections).",
	},
)

// dbConnectionsIdle tracks the number of idle connections reported by
// sql.DBStats.Idle. Updated by StartDBStatsCollector.
var dbConnectionsIdle = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "nf_db_connections_idle",
		Help: "Number of idle database connections (from sql.DBStats.Idle).",
	},
)

func init() {
	prometheus.MustRegister(dbQueryDuration)
	prometheus.MustRegister(dbQueriesTotal)
	prometheus.MustRegister(dbConnectionsOpen)
	prometheus.MustRegister(dbConnectionsIdle)
}

// RecordDBQuery records the duration and outcome of a single database query.
// The query parameter should be the sqlc operation name (e.g. "ListTasks",
// "CreateTask"). Pass the error returned by the query; a nil error records
// status "ok", otherwise "error".
func RecordDBQuery(query string, duration time.Duration, err error) {
	dbQueryDuration.WithLabelValues(query).Observe(duration.Seconds())
	status := "ok"
	if err != nil {
		status = "error"
	}
	dbQueriesTotal.WithLabelValues(query, status).Inc()
}

// StartDBStatsCollector launches a background goroutine that polls db.Stats()
// every 15 seconds and updates the nf_db_connections_open and
// nf_db_connections_idle gauges. The goroutine exits when the provided done
// channel is closed.
func StartDBStatsCollector(db *sql.DB, done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				stats := db.Stats()
				dbConnectionsOpen.Set(float64(stats.OpenConnections))
				dbConnectionsIdle.Set(float64(stats.Idle))
			}
		}
	}()
}

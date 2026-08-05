// Package dbtz pins the timezone a MySQL session interprets times in.
//
// Every DATETIME this product stores is a UTC wall clock: the driver is
// configured with Loc=UTC, and Go writes the UTC components of a
// time.Time. Queries then compare those columns against NOW(), which
// answers in the session's timezone — and a session inherits the
// server's, which inherits the host's.
//
// On a deployment whose MySQL host is not UTC the two disagree by the
// offset, and nothing reports it. At Asia/Tokyo, NOW() runs nine hours
// ahead of the stored values, so:
//
//   - OAuth states expire nine hours early and every GitHub, Google and
//     Slack callback is rejected as stale;
//   - webhook backoff windows are already in the past, so a failing
//     subscription burns through max_attempts immediately;
//   - magic links and invites vanish nine hours before they should;
//   - reminders fire at the wrong hour.
//
// None of it raises an error. The queries are valid, the rows are there,
// and the comparisons are simply wrong — which is why the session
// timezone is pinned in the DSN and then asserted, rather than left to
// whichever machine the database happens to run on.
package dbtz

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

// SessionTimezone is the value every connection must report. Quoted
// because the DSN carries it as a literal SQL string.
const SessionTimezone = "+00:00"

// dsnParam is the go-sql-driver parameter that issues
// `SET time_zone = ...` on each new connection.
const dsnParam = "time_zone"

// NormalizeDSN returns dsn with the session timezone pinned to UTC,
// adding the parameter when it is absent and replacing it when it names
// anything else.
//
// Overriding rather than respecting an existing value is deliberate: a
// DSN that pins a different zone is a misconfiguration, not a
// preference, because the stored values are UTC regardless. Silently
// honouring it would reintroduce exactly the drift this package exists
// to remove.
func NormalizeDSN(dsn string) string {
	if dsn == "" {
		return dsn
	}
	base, params, found := strings.Cut(dsn, "?")
	want := dsnParam + "=" + url.QueryEscape("'"+SessionTimezone+"'")
	if !found || params == "" {
		return base + "?" + want
	}
	kept := make([]string, 0, 8)
	for _, kv := range strings.Split(params, "&") {
		if kv == "" {
			continue
		}
		key, _, _ := strings.Cut(kv, "=")
		if key == dsnParam {
			continue
		}
		kept = append(kept, kv)
	}
	kept = append(kept, want)
	return base + "?" + strings.Join(kept, "&")
}

// AssertUTCSession verifies that the connection pool really does report
// a zero offset, and returns an error naming what it found otherwise.
//
// The DSN parameter is the fix; this is the check that the fix is in
// force. A deployment can reach the database through a proxy that
// rewrites connection options, or through a DSN assembled somewhere this
// package never saw, and the failure mode is silent in every one of
// those cases. Calling this at startup converts a wrong answer into a
// refusal to start.
//
// The value is read as an offset rather than compared as a string:
// MySQL reports the session zone as either an offset or the literal
// SYSTEM, and SYSTEM can still be UTC.
func AssertUTCSession(ctx context.Context, db *sql.DB) error {
	var offset string
	if err := db.QueryRowContext(ctx,
		`SELECT TIMEDIFF(NOW(), UTC_TIMESTAMP())`).Scan(&offset); err != nil {
		return fmt.Errorf("dbtz: read session offset: %w", err)
	}
	switch offset {
	case "00:00:00", "0:00:00":
		return nil
	}
	return fmt.Errorf(
		"dbtz: MySQL session is %s away from UTC; stored values are UTC wall clocks, "+
			"so every NOW() comparison is off by that much. Pin %s='%s' in the DSN "+
			"(or --default-time-zone=%s on the server)",
		offset, dsnParam, SessionTimezone, SessionTimezone)
}

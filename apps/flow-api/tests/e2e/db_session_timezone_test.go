package e2e

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtz"
)

// TestSessionTimezoneIsUTC pins the invariant every NOW() comparison in
// this codebase rests on.
//
// Stored DATETIMEs are UTC wall clocks — the driver writes the UTC
// components of a time.Time — and queries compare them against NOW(),
// which answers in the session's zone. If the two ever diverge the
// queries stay valid and the answers become wrong by the offset: OAuth
// states expire early, webhook backoff windows read as already past,
// invites disappear ahead of time, reminders fire at the wrong hour.
// Nothing logs.
func TestSessionTimezoneIsUTC(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	require.NoError(t, dbtz.AssertUTCSession(context.Background(), testDB),
		"the test database session must be UTC, like production")
}

// TestAssertUTCSessionRejectsAShiftedSession proves the check would
// actually notice. A test that only asserts the happy path passes on a
// build where AssertUTCSession returns nil unconditionally.
func TestAssertUTCSessionRejectsAShiftedSession(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()

	// A pool of one connection, so the SET below is guaranteed to be the
	// session the assertion then reads.
	shifted, err := sql.Open("mysql", helpers.StartShared(t).DSN)
	require.NoError(t, err)
	defer func() { _ = shifted.Close() }()
	shifted.SetMaxOpenConns(1)

	_, err = shifted.ExecContext(ctx, `SET SESSION time_zone = '+09:00'`)
	require.NoError(t, err)

	err = dbtz.AssertUTCSession(ctx, shifted)
	require.Error(t, err, "a session nine hours off UTC must be refused")
	require.Contains(t, err.Error(), "away from UTC")
}

package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var sharedDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "inbox_handler_test"})

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := sharedDB.Start(context.Background())
	require.NoError(t, err)
	return inst.DB
}

// tenant is one workspace with one member, which is what an inbox
// operation needs before it can reach the queue.
type tenant struct {
	workspaceID     uint32
	workspacePublic string
	userID          uint32
}

func seed(ctx context.Context, t *testing.T, db *sql.DB) tenant {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	run := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	// workspaces.slug is globally unique and cut to ten characters, so it
	// is taken off the low-order end: the leading digits of a nanosecond
	// timestamp only change once a second.
	wsPub := types.New()
	wsID := run(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "ws-"+suffix[len(suffix)-10:], "Inbox "+suffix)
	userID := run(`INSERT INTO users (public_id, email, display_name, locale, timezone)
		VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), "inbox+"+suffix+"@example.test", "Inbox Tester")
	run(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		types.New(), wsID, userID)

	return tenant{workspaceID: wsID, workspacePublic: wsPub.String(), userID: userID}
}

// seedInboxItem writes the signals row the inbox projects, and returns
// its public id — the only id an inbox operation is addressed by.
func seedInboxItem(ctx context.Context, t *testing.T, db *sql.DB, tn tenant) string {
	t.Helper()
	pub := types.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		 VALUES (?, ?, 'manual', 'manual', '{}', ?, 'workspace')`,
		pub, tn.workspaceID, time.Now().UTC())
	require.NoError(t, err)
	return pub.String()
}

// trail is the pair of counts every assertion here is about, scoped to
// the test's own workspace so a parallel run cannot change the answer.
type trail struct {
	events int
	audits int
}

func readTrail(ctx context.Context, t *testing.T, db *sql.DB, tn tenant, kind eventbus.Kind, action string) trail {
	t.Helper()
	var c trail
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`,
		tn.workspaceID, string(kind)).Scan(&c.events))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		tn.workspaceID, action).Scan(&c.audits))
	return c
}

func deps(db *sql.DB) Deps {
	q := generated.New(db)
	return Deps{DB: db, Queries: q, Mutations: mutationlog.New(db, q)}
}

// TestArchiveRecordsBothLogs covers the write that takes an item off
// every member's queue. It is shared workspace state, so it has to be
// findable afterwards on the timeline and by an audit query on the
// action name.
func TestArchiveRecordsBothLogs(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	itemID := seedInboxItem(ctx, t, db, tn)
	actorCtx := middleware.WithActor(ctx, tn.userID)

	const action = "inbox.archive"
	before := readTrail(ctx, t, db, tn, eventbus.SignalArchived, action)
	out, err := Archive(deps(db))(actorCtx, &ArchiveInboxInput{ID: itemID, WorkspaceID: tn.workspacePublic})
	require.NoError(t, err)
	require.True(t, out.Body.Ok)

	after := readTrail(ctx, t, db, tn, eventbus.SignalArchived, action)
	require.Equal(t, before.events+1, after.events,
		"archiving removes the item from every member's queue, so it must appear on the timeline")
	require.Equal(t, before.audits+1, after.audits,
		"archiving must answer an audit query on the action, or nobody can say who emptied the queue")

	var payload string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, string(eventbus.SignalArchived)).Scan(&payload))
	require.JSONEq(t, `{"inboxItemId":"`+itemID+`"}`, payload,
		"the payload identifies the item by its public id and carries nothing internal")
}

// TestSnoozeRecordsBothLogs covers the other write that changes what
// every member sees, with the deadline the queue will resurface it at.
func TestSnoozeRecordsBothLogs(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	itemID := seedInboxItem(ctx, t, db, tn)
	actorCtx := middleware.WithActor(ctx, tn.userID)

	const action = "inbox.snooze"
	until := time.Now().Add(24 * time.Hour).Unix()
	before := readTrail(ctx, t, db, tn, eventbus.SignalSnoozed, action)
	in := &SnoozeInboxInput{ID: itemID, WorkspaceID: tn.workspacePublic}
	in.Body.SnoozeUntil = until
	out, err := Snooze(deps(db))(actorCtx, in)
	require.NoError(t, err)
	require.True(t, out.Body.Ok)

	after := readTrail(ctx, t, db, tn, eventbus.SignalSnoozed, action)
	require.Equal(t, before.events+1, after.events,
		"snoozing hides the item from every member's queue, so it must appear on the timeline")
	require.Equal(t, before.audits+1, after.audits,
		"snoozing must answer an audit query on the action, or the deferral is recorded nowhere")

	var eventPayload, auditMetadata string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, string(eventbus.SignalSnoozed)).Scan(&eventPayload))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT metadata_json FROM audit_logs WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, action).Scan(&auditMetadata))
	require.JSONEq(t, eventPayload, auditMetadata,
		"both rows describe one change, so they carry one description")
	require.JSONEq(t, fmt.Sprintf(`{"inboxItemId":%q,"snoozeUntil":%d}`, itemID, until), eventPayload)
}

// TestArchiveRecordsNothingWhenTheItemIsAlreadyGone pairs with
// [TestArchiveRecordsBothLogs]: the handler answers a second archive of
// the same item with a 404 because it changed nothing, and a change that
// did not happen must not be logged as one.
func TestArchiveRecordsNothingWhenTheItemIsAlreadyGone(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	itemID := seedInboxItem(ctx, t, db, tn)
	actorCtx := middleware.WithActor(ctx, tn.userID)

	const action = "inbox.archive"
	_, err := Archive(deps(db))(actorCtx, &ArchiveInboxInput{ID: itemID, WorkspaceID: tn.workspacePublic})
	require.NoError(t, err)

	afterFirst := readTrail(ctx, t, db, tn, eventbus.SignalArchived, action)
	_, err = Archive(deps(db))(actorCtx, &ArchiveInboxInput{ID: itemID, WorkspaceID: tn.workspacePublic})
	require.Error(t, err, "the item is no longer in the inbox, so there is nothing left to archive")

	require.Equal(t, afterFirst, readTrail(ctx, t, db, tn, eventbus.SignalArchived, action),
		"a request that archived nothing must leave no trace claiming it did")
}

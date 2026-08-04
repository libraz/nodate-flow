// Integration tests for the per-recipient channel fan-out behaviour.
// These exercise the real notification_preferences table against the
// shared MySQL testcontainer so the SQL slice expansion, ENUM coercion
// and (recipient, source_event, channel) UNIQUE dedupe are all covered
// end-to-end.
package notification

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// fanoutFixture is the minimum tenant scaffolding the fan-out path
// needs: a workspace, an actor (excluded from delivery), and a
// recipient that the test can attach preference rows to.
type fanoutFixture struct {
	wsID            uint32
	actorUserID     uint32
	recipientUserID uint32
	taskInternalID  uint32
	eventInternalID uint64
}

func seedFanoutFixture(t *testing.T, db *sql.DB) *fanoutFixture {
	t.Helper()
	ctx := context.Background()

	suffix := uuid.New().String()[:8]
	insertUser := func(label string) uint32 {
		pub := uuid.Must(uuid.NewV7())
		res, err := db.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale)
			 VALUES (?, ?, ?, 'en')`,
			pub[:], "fanout-"+label+"-"+suffix+"@example.test", "Fanout "+label)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	actor := insertUser("actor")
	recipient := insertUser("recipient")

	wsPub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "ws-fanout-"+suffix, "Workspace fanout")
	require.NoError(t, err)
	wsRaw, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	insertMember := func(userID uint32, role string) {
		pub := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
			 VALUES (?, ?, ?, ?)`,
			pub[:], wsID, userID, role)
		require.NoError(t, err)
	}
	insertMember(actor, "owner")
	insertMember(recipient, "member")

	prjPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name)
		 VALUES (?, ?, ?, ?)`,
		prjPub[:], wsID, "prj-fanout-"+suffix, "Project fanout")
	require.NoError(t, err)
	prjRaw, err := res.LastInsertId()
	require.NoError(t, err)
	prjID := uint32(prjRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	taskPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, 1, 'Test task', 'public', ?)`,
		taskPub[:], wsID, prjID, actor)
	require.NoError(t, err)
	taskRaw, err := res.LastInsertId()
	require.NoError(t, err)
	taskID := uint32(taskRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Append a real events row so eventByID can resolve the actor and
	// resource_public_id exactly the way production does.
	eventPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, type, actor_user_id, task_id, payload_json, occurred_at)
		 VALUES (?, ?, 'task.created', ?, ?, JSON_OBJECT(), NOW(3))`,
		eventPub[:], wsID, actor, taskID)
	require.NoError(t, err)
	eventRaw, err := res.LastInsertId()
	require.NoError(t, err)
	eventID := uint64(eventRaw) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative

	return &fanoutFixture{
		wsID:            wsID,
		actorUserID:     actor,
		recipientUserID: recipient,
		taskInternalID:  taskID,
		eventInternalID: eventID,
	}
}

// upsertPreference writes a single notification_preferences row for
// the given (user, workspace, category, channel) tuple. Tests use this
// to opt the recipient in or out of specific delivery channels before
// firing fan-out.
func upsertPreference(t *testing.T, db *sql.DB, wsID, userID uint32, category, channel string, isMuted bool) {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO notification_preferences
		   (public_id, workspace_id, user_id, event_category, channel, is_muted)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		pub[:], wsID, userID, category, channel, isMuted)
	require.NoError(t, err)
}

// listChannelsForRecipient returns the set of notifications.channel
// values that exist for the (workspace, recipient, source_event)
// triple. The tests use it as the post-fan-out assertion vector.
func listChannelsForRecipient(t *testing.T, db *sql.DB, wsID, recipientID uint32, eventID uint64) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT channel
		   FROM notifications
		  WHERE workspace_id = ?
		    AND recipient_user_id = ?
		    AND source_event_id = ?
		  ORDER BY channel`,
		wsID, recipientID, eventID)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var ch string
		require.NoError(t, rows.Scan(&ch))
		got = append(got, ch)
	}
	require.NoError(t, rows.Err())
	return got
}

// runFanout invokes the production fanout method synchronously so the
// test can assert on the resulting notification rows without waiting
// on goroutine scheduling.
func runFanout(t *testing.T, db *sql.DB, fx *fanoutFixture, eventType string) *Fanout {
	t.Helper()
	q := generated.New(db)
	f := NewFanout(db, q, nil)
	f.fanout(context.Background(), fx.wsID, eventType, fx.eventInternalID)
	return f
}

// TestFanout_RespectsExplicitChannelPreference verifies that an
// explicit (email enabled, in_app absent) preference results in
// exactly one email notification and zero in_app notifications,
// proving the historical "always in_app" hardcode is gone.
func TestFanout_RespectsExplicitChannelPreference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fanout DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	// Opt the recipient into email only for the lifecycle category
	// that "task.created" maps to.
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Equal(t, []string{"email"}, got)
}

// TestFanout_DefaultsToInAppWhenUnconfigured verifies that recipients
// with no rows in notification_preferences still receive a single
// in_app notification — the historical default that downstream
// surfaces (badge counts, bell list) depend on.
func TestFanout_DefaultsToInAppWhenUnconfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fanout DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Equal(t, []string{"in_app"}, got)
}

// TestFanout_MultipleEnabledChannelsProducePerChannelRows verifies
// that opting into both in_app and email yields two distinct rows
// (one per channel) for the same source event.
func TestFanout_MultipleEnabledChannelsProducePerChannelRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fanout DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", false)
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.ElementsMatch(t, []string{"email", "in_app"}, got)
}

// TestFanout_RefireIsNoopPerChannel verifies that re-running fan-out
// for the same event yields zero net new rows because the
// (recipient, source_event, channel) UNIQUE key collides for every
// channel that was already produced on the first run.
func TestFanout_RefireIsNoopPerChannel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fanout DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", false)
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")
	first := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)

	runFanout(t, db, fx, "task.created")
	second := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)

	require.Equal(t, first, second)
	require.Len(t, second, 2)
}

// TestFanout_MutedPreferenceSuppressesChannel verifies that an
// is_muted=TRUE preference behaves identically to a missing row for
// that channel — the muted channel must not produce a notification,
// even if the unmuted alternative is also enabled.
func TestFanout_MutedPreferenceSuppressesChannel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fanout DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", true)
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Equal(t, []string{"email"}, got)
}

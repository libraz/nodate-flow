// Integration tests for the per-recipient channel fan-out behaviour.
// These exercise the real notification_preferences table against the
// shared MySQL testcontainer so the SQL slice expansion, ENUM coercion
// and (recipient, source_event, channel) UNIQUE dedupe are all covered
// end-to-end.
package notification

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
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

// publishRecorder captures the workspaces a fan-out pass announced on
// the realtime stream, standing in for
// stream.EventbusTap.PublishNotification.
type publishRecorder struct {
	mu  sync.Mutex
	ids []uint32
}

func (r *publishRecorder) publish(_ context.Context, workspaceID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, workspaceID)
}

func (r *publishRecorder) announced() []uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ids)
}

// runFanoutRecordingPublishes runs the same production fan-out method as
// [runFanout] with the realtime publisher captured, so a test can assert
// on what the pass announced alongside what it wrote.
func runFanoutRecordingPublishes(t *testing.T, db *sql.DB, fx *fanoutFixture, eventType string) *publishRecorder {
	t.Helper()
	rec := &publishRecorder{}
	f := NewFanout(db, generated.New(db), nil)
	f.SetNotificationPublisher(rec.publish)
	f.fanout(context.Background(), fx.wsID, eventType, fx.eventInternalID)
	return rec
}

// TestFanout_AnnouncesAPassThatWroteRows is the positive half of the
// realtime contract: the frontend maps notification.changed onto the
// bell list and the unread badge, so a pass that writes a row and says
// nothing leaves both stale until something unrelated refetches.
//
// The workspace id is asserted, not just the count, because the wire
// event is addressed by workspace and an event addressed to the wrong
// one reaches nobody just as silently.
func TestFanout_AnnouncesAPassThatWroteRows(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	rec := runFanoutRecordingPublishes(t, db, fx, "task.created")

	require.Equal(t, []string{"in_app"},
		listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID),
		"fixture must write a row for the announcement to be about something")
	require.Equal(t, []uint32{fx.wsID}, rec.announced())
}

// TestFanout_AnnouncesNothingWhenNobodyIsNotified is the pairing
// assertion. It differs from TestFanout_AnnouncesAPassThatWroteRows by
// the mute alone, so the two together show one code path deciding both
// ways rather than a publisher that is simply never called.
//
// Announcing here would wake every client subscribed to the workspace to
// re-read a bell that gained nothing, and most declared event kinds are
// classified silent — so the cost is not an edge case.
func TestFanout_AnnouncesNothingWhenNobodyIsNotified(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", true)

	rec := runFanoutRecordingPublishes(t, db, fx, "task.created")

	require.Empty(t,
		listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID),
		"fixture must write no row for the silence to be the property under test")
	require.Empty(t, rec.announced())
}

// TestFanout_AnnouncesOnlyTheRefireThatWroteSomething pins the gate to
// rows created rather than rows attempted. A re-fired hook builds the
// same batch and the unique key collapses all of it, so an announcement
// on the second pass would describe rows the first pass already
// announced.
func TestFanout_AnnouncesOnlyTheRefireThatWroteSomething(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	first := runFanoutRecordingPublishes(t, db, fx, "task.created")
	require.Equal(t, []uint32{fx.wsID}, first.announced())

	second := runFanoutRecordingPublishes(t, db, fx, "task.created")
	require.Empty(t, second.announced())

	require.Equal(t, []string{"in_app"},
		listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID),
		"the refire must not have created a second row either")
}

// TestFanout_RespectsExplicitChannelPreference verifies that a
// recipient who has muted in_app and opted into email receives exactly
// one email notification and zero in_app ones, proving the historical
// "always in_app" hardcode is gone.
//
// The in_app row is explicit. Fan-out reads the absence of a row as the
// channel default, so leaving it out states nothing about in_app — and
// the default for in_app is to deliver.
func TestFanout_RespectsExplicitChannelPreference(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	// Route the recipient's lifecycle notifications (the category
	// "task.created" maps to) to email instead of the bell.
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", true)
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Equal(t, []string{"email"}, got)
}

// TestFanout_EmailOptInKeepsInApp pins the per-channel nature of the
// defaults: adding a channel is not the same as replacing the set.
//
// A recipient who asks for email and says nothing about in_app keeps
// the bell, because a row for one channel says nothing about another.
// Reading "has any row" as "has configured every channel" is what made
// a mute on the default channel unrepresentable, so this is the pairing
// assertion to TestFanout_MutedCategoryProducesNoRows.
func TestFanout_EmailOptInKeepsInApp(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.ElementsMatch(t, []string{"email", "in_app"}, got)
}

// TestFanout_MutedCategoryProducesNoRows is the setting doing its job:
// a recipient who has muted every channel of a category gets nothing at
// all for it.
//
// Producing zero rows is a legitimate outcome of fan-out, not an error
// path — the earlier shape treated an empty resolved channel set as
// "unconfigured" and fell back to in_app, which is precisely how a mute
// was thrown away.
func TestFanout_MutedCategoryProducesNoRows(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", true)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Empty(t, got)
}

// TestFanout_DefaultsToInAppWhenUnconfigured verifies that recipients
// with no rows in notification_preferences still receive a single
// in_app notification — the historical default that downstream
// surfaces (badge counts, bell list) depend on.
func TestFanout_DefaultsToInAppWhenUnconfigured(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
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
	testhelpers.SkipUnlessIntegration(t)
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
	testhelpers.SkipUnlessIntegration(t)
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
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "in_app", true)
	upsertPreference(t, db, fx.wsID, fx.recipientUserID, "task.lifecycle", "email", false)

	runFanout(t, db, fx, "task.created")

	got := listChannelsForRecipient(t, db, fx.wsID, fx.recipientUserID, fx.eventInternalID)
	require.Equal(t, []string{"email"}, got)
}

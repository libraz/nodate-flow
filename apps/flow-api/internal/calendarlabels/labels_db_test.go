package calendarlabels_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type fixture struct {
	db         *sql.DB
	wsID       uint32
	calendarID uint32
	masterID   uint32
	enabledID  uint32
	disabledID uint32
	labelA     uint32
	labelB     uint32
}

func seed(t *testing.T, db *sql.DB) fixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	insert := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}
	fx := fixture{db: db}
	fx.wsID = insert(`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, 'Labels')`, types.New(), "labels-"+suffix)
	user := insert(`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, 'Labels', 'en')`,
		types.New(), "labels-"+suffix+"@example.test")
	fx.calendarID = insert(`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', 'Labels')`,
		types.New(), fx.wsID)
	start := time.Date(2031, 5, 5, 9, 0, 0, 0, time.UTC)
	event := func(parent any, original any, enabled bool) uint32 {
		return insert(`INSERT INTO calendar_events
			   (public_id, workspace_id, calendar_id, recurrence_parent_id, recurrence_original_start,
			    title, start_at, end_at, timezone, owner_user_id, created_by_user_id, enabled)
			 VALUES (?, ?, ?, ?, ?, 'E', ?, ?, 'UTC', ?, ?, ?)`,
			types.New(), fx.wsID, fx.calendarID, parent, original, start, start.Add(time.Hour), user, user, enabled)
	}
	fx.masterID = event(nil, nil, true)
	fx.enabledID = event(fx.masterID, start.AddDate(0, 0, 1), true)
	fx.disabledID = event(fx.masterID, start.AddDate(0, 0, 2), false)
	label := func(name string) uint32 {
		return insert(`INSERT INTO calendar_labels (public_id, workspace_id, calendar_id, name, color) VALUES (?, ?, ?, ?, '#112233')`,
			types.New(), fx.wsID, fx.calendarID, name)
	}
	fx.labelA, fx.labelB = label("A"), label("B")
	return fx
}

func (fx fixture) updatedAt(t *testing.T, id uint32) time.Time {
	t.Helper()
	var at time.Time
	require.NoError(t, fx.db.QueryRow(`SELECT updated_at FROM calendar_events WHERE id = ?`, id).Scan(&at))
	return at
}

func TestSetLinkWritesOnlyOnChange(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	fx := seed(t, db)
	q := calendar.New(db)

	changed, refusal, err := calendarlabels.SetLink(ctx, q, fx.wsID, fx.masterID, fx.labelA)
	require.NoError(t, err)
	require.Nil(t, refusal)
	require.True(t, changed)
	stamp := fx.updatedAt(t, fx.masterID)

	time.Sleep(20 * time.Millisecond)
	changed, refusal, err = calendarlabels.SetLink(ctx, q, fx.wsID, fx.masterID, fx.labelA)
	require.NoError(t, err)
	require.Nil(t, refusal)
	require.False(t, changed, "the same value writes nothing")
	require.True(t, fx.updatedAt(t, fx.masterID).Equal(stamp), "and does not advance updated_at")

	changed, _, err = calendarlabels.SetLink(ctx, q, fx.wsID, fx.masterID, fx.labelB)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, fx.updatedAt(t, fx.masterID).After(stamp))
	got, err := calendarlabels.CurrentLabel(ctx, q, fx.masterID)
	require.NoError(t, err)
	require.Equal(t, fx.labelB, got)

	changed, _, err = calendarlabels.SetLink(ctx, q, fx.wsID, fx.masterID, 0)
	require.NoError(t, err)
	require.True(t, changed)
	got, err = calendarlabels.CurrentLabel(ctx, q, fx.masterID)
	require.NoError(t, err)
	require.Zero(t, got)

	changed, _, err = calendarlabels.SetLink(ctx, q, fx.wsID, fx.masterID, 0)
	require.NoError(t, err)
	require.False(t, changed, "clearing an unlabelled event is a no-op")
}

func TestSetLinkDeletedLabelIsUnknown(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	fx := seed(t, db)
	_, err := db.Exec(`DELETE FROM calendar_labels WHERE id = ?`, fx.labelA)
	require.NoError(t, err)

	changed, refusal, err := calendarlabels.SetLink(context.Background(), calendar.New(db), fx.wsID, fx.masterID, fx.labelA)
	require.NoError(t, err)
	require.False(t, changed)
	require.NotNil(t, refusal)
	require.Equal(t, apierrors.CalendarEventLabelUnknown, refusal.Spec)
}

func TestResolveLabelScopedToCalendar(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	fx := seed(t, db)
	other := seed(t, db)
	q := calendar.New(db)

	var pub types.PublicID
	require.NoError(t, db.QueryRow(`SELECT public_id FROM calendar_labels WHERE id = ?`, fx.labelA).Scan(&pub))

	id, refusal, err := calendarlabels.ResolveLabel(ctx, q, fx.calendarID, pub.String())
	require.NoError(t, err)
	require.Nil(t, refusal)
	require.Equal(t, fx.labelA, id)

	for name, raw := range map[string]string{"malformed": "nope", "missing": types.New().String()} {
		_, refusal, err = calendarlabels.ResolveLabel(ctx, q, fx.calendarID, raw)
		require.NoError(t, err, name)
		require.NotNil(t, refusal, name)
		require.Equal(t, apierrors.CalendarEventLabelUnknown, refusal.Spec, name)
	}
	_, refusal, err = calendarlabels.ResolveLabel(ctx, q, other.calendarID, pub.String())
	require.NoError(t, err)
	require.NotNil(t, refusal, "another calendar's label is unknown")
}

func TestApplyToSeriesReachesOnlyEnabledOverrides(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	fx := seed(t, db)
	q := calendar.New(db)

	_, _, err := calendarlabels.SetLink(ctx, q, fx.wsID, fx.enabledID, fx.labelB)
	require.NoError(t, err)

	changed, refusal, err := calendarlabels.ApplyToSeries(ctx, q, fx.wsID, fx.masterID,
		calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: fx.labelA})
	require.NoError(t, err)
	require.Nil(t, refusal)
	require.True(t, changed)

	label := func(id uint32) uint32 {
		v, err := calendarlabels.CurrentLabel(ctx, q, id)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, fx.labelA, label(fx.masterID))
	require.Equal(t, fx.labelA, label(fx.enabledID), "an enabled override's own label is overwritten")
	require.Zero(t, label(fx.disabledID), "a disabled override is untouched")

	changed, _, err = calendarlabels.ApplyToSeries(ctx, q, fx.wsID, fx.masterID, calendarlabels.Change{})
	require.NoError(t, err)
	require.False(t, changed, "Keep does nothing")

	changed, _, err = calendarlabels.ApplyToSeries(ctx, q, fx.wsID, fx.masterID, calendarlabels.Change{Mode: calendarlabels.ChangeClear})
	require.NoError(t, err)
	require.True(t, changed)
	require.Zero(t, label(fx.masterID))
	require.Zero(t, label(fx.enabledID))
}

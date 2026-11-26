package calendarlabels

import (
	"context"
	"database/sql"
	"errors"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// mysqlFKViolation is ER_NO_REFERENCED_ROW_2.
const mysqlFKViolation = 1452

// ChangeMode says what a write does to an event's label.
type ChangeMode int

const (
	// ChangeKeep leaves the label as it is. The zero value.
	ChangeKeep ChangeMode = iota
	// ChangeSet files the event under Change.LabelID.
	ChangeSet
	// ChangeClear removes the label.
	ChangeClear
)

// Change is a label instruction already resolved to an internal id.
type Change struct {
	Mode    ChangeMode
	LabelID uint32 // meaningful for ChangeSet only
}

// Value is the label id the change writes: 0 for clear (and keep).
func (c Change) Value() uint32 {
	if c.Mode == ChangeSet {
		return c.LabelID
	}
	return 0
}

// Querier is the subset of calendar queries this package uses; a
// transaction-bound *calendar.Queries satisfies it.
type Querier interface {
	FindCalendarLabelByPublicId(ctx context.Context, arg calendar.FindCalendarLabelByPublicIdParams) (calendar.FindCalendarLabelByPublicIdRow, error)
	GetEventLabel(ctx context.Context, eventID uint32) (uint32, error)
	SetEventLabel(ctx context.Context, arg calendar.SetEventLabelParams) error
	DeleteEventLabel(ctx context.Context, eventID uint32) (int64, error)
	TouchCalendarEvent(ctx context.Context, arg calendar.TouchCalendarEventParams) error
	ListEnabledOverrideIdsOfSeries(ctx context.Context, arg calendar.ListEnabledOverrideIdsOfSeriesParams) ([]uint32, error)
}

// ResolveLabel maps a label's public id to its internal id within one
// calendar. A malformed id, a missing label and another calendar's label
// all answer CALENDAR.EVENT.LABEL_UNKNOWN.
func ResolveLabel(ctx context.Context, q Querier, calendarID uint32, raw string) (uint32, *apierrors.APIError, error) {
	pub, err := types.Parse(raw)
	if err != nil {
		return 0, apierrors.New(apierrors.CalendarEventLabelUnknown), nil
	}
	row, err := q.FindCalendarLabelByPublicId(ctx, calendar.FindCalendarLabelByPublicIdParams{PublicID: pub, CalendarID: calendarID})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, apierrors.New(apierrors.CalendarEventLabelUnknown), nil
	}
	if err != nil {
		return 0, nil, err
	}
	return row.ID, nil, nil
}

// RequireDetailsVisible refuses a label write by a viewer who cannot see
// the event's details.
func RequireDetailsVisible(visible bool) *apierrors.APIError {
	if visible {
		return nil
	}
	return apierrors.New(apierrors.CalendarEventDetailsHidden)
}

// CurrentLabel is the label id an event row carries, or 0 for none.
func CurrentLabel(ctx context.Context, q Querier, eventID uint32) (uint32, error) {
	id, err := q.GetEventLabel(ctx, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// SetLink is the only writer of an event row's label link; labelID 0 means
// no label. It writes nothing when the link already holds that value, and
// otherwise writes it and advances the row's updated_at. A label deleted
// concurrently (foreign key violation) answers LABEL_UNKNOWN.
func SetLink(ctx context.Context, q Querier, workspaceID, eventID, labelID uint32) (bool, *apierrors.APIError, error) {
	current, err := CurrentLabel(ctx, q, eventID)
	if err != nil {
		return false, nil, err
	}
	if current == labelID {
		return false, nil, nil
	}
	if labelID == 0 {
		// affected-rows: not-applicable — zero means the link was already
		// gone, which is the state the caller asked for.
		if _, err := q.DeleteEventLabel(ctx, eventID); err != nil {
			return false, nil, err
		}
	} else if err := q.SetEventLabel(ctx, calendar.SetEventLabelParams{EventID: eventID, LabelID: labelID}); err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == mysqlFKViolation {
			return false, apierrors.New(apierrors.CalendarEventLabelUnknown), nil
		}
		return false, nil, err
	}
	if err := q.TouchCalendarEvent(ctx, calendar.TouchCalendarEventParams{ID: eventID, WorkspaceID: workspaceID}); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

// ApplyToSeries applies c to a master (or a single event) and, when that
// changed it, to each of the series' enabled overrides, overwriting their
// own labels. Disabled overrides are left alone; their value is decided
// when they are revived.
func ApplyToSeries(ctx context.Context, q Querier, workspaceID, masterID uint32, c Change) (bool, *apierrors.APIError, error) {
	if c.Mode == ChangeKeep {
		return false, nil, nil
	}
	value := c.Value()
	changed, refusal, err := SetLink(ctx, q, workspaceID, masterID, value)
	if err != nil || refusal != nil || !changed {
		return false, refusal, err
	}
	ids, err := q.ListEnabledOverrideIdsOfSeries(ctx, calendar.ListEnabledOverrideIdsOfSeriesParams{
		RecurrenceParentID: sql.NullInt32{Int32: int32(masterID), Valid: true}, //#nosec G115 -- internal row id
		WorkspaceID:        workspaceID,
	})
	if err != nil {
		return false, nil, err
	}
	for _, id := range ids {
		if _, refusal, err := SetLink(ctx, q, workspaceID, id, value); err != nil || refusal != nil {
			return false, refusal, err
		}
	}
	return true, nil, nil
}

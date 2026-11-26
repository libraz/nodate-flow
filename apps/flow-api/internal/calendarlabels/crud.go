package calendarlabels

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const (
	// MaxPerCalendar is the number of labels one calendar may hold.
	MaxPerCalendar = 50
	// MaxNameRunes is the longest label name, matching the column width.
	MaxNameRunes = 50
	// MaxSummaryRunes bounds the label name copied into a record payload.
	MaxSummaryRunes = 500
)

// errAnswered rolls a transaction back once the refusal is decided. It is
// not transient, so dbretry does not retry it.
var errAnswered = errors.New("calendarlabels: answered")

// Label is a calendar label with its times left as time.Time; a transport
// converts them at its own boundary.
type Label struct {
	ID       uint32 // internal row id, never exposed
	PublicID types.PublicID
	Name     string
	Color    string
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last edit, or CreatedAt until the first edit.
	UpdatedAt time.Time
}

// FromFields builds a Label from a label row's columns. updated_at is NULL
// until the first edit, in which case the creation time stands in for it.
func FromFields(id uint32, publicID types.PublicID, name, color string, updatedAt sql.NullTime, createdAt time.Time) Label {
	l := Label{ID: id, PublicID: publicID, Name: name, Color: color, CreatedAt: createdAt, UpdatedAt: createdAt}
	if updatedAt.Valid {
		l.UpdatedAt = updatedAt.Time
	}
	return l
}

// FromListRow maps a list row to a Label.
func FromListRow(r calendar.ListCalendarLabelsRow) Label {
	return FromFields(r.ID, r.PublicID, r.Name, r.Color, r.UpdatedAt, r.CreatedAt)
}

// FromFindRow maps a find row to a Label.
func FromFindRow(r calendar.FindCalendarLabelByPublicIdRow) Label {
	return FromFields(r.ID, r.PublicID, r.Name, r.Color, r.UpdatedAt, r.CreatedAt)
}

// NormalizeName trims Unicode whitespace (full-width included) and reports
// whether what is left is a usable name: 1 to [MaxNameRunes] runes, no
// control characters.
func NormalizeName(raw string) (string, bool) {
	name := strings.TrimFunc(raw, unicode.IsSpace)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

// Summary is the label name as carried in a record payload, cut to
// [MaxSummaryRunes].
func Summary(name string) string {
	if utf8.RuneCountInString(name) <= MaxSummaryRunes {
		return name
	}
	return string([]rune(name)[:MaxSummaryRunes])
}

func invalidName() *apierrors.APIError {
	return apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", "name")
}

// Find resolves a label of the calendar by its public id. A malformed,
// unknown or other calendar's id is the same CALENDAR.LABEL.NOT_FOUND.
func Find(ctx context.Context, q Querier, calendarID uint32, labelID string) (calendar.FindCalendarLabelByPublicIdRow, *apierrors.APIError) {
	pid, err := types.Parse(labelID)
	if err != nil {
		return calendar.FindCalendarLabelByPublicIdRow{}, apierrors.New(apierrors.CalendarLabelNotFound)
	}
	row, err := q.FindCalendarLabelByPublicId(ctx, calendar.FindCalendarLabelByPublicIdParams{PublicID: pid, CalendarID: calendarID})
	if err != nil {
		return row, apierrors.New(apierr.SpecForErrNoRows(err, apierrors.CalendarLabelNotFound, apierrors.CalendarLabelStoreReadInterrupted))
	}
	return row, nil
}

// AfterWrite runs inside the write transaction once the label is written,
// with the label as stored. An error rolls the write back and is answered as
// the operation's store-write refusal. A nil hook does nothing.
type AfterWrite func(ctx context.Context, tx *dbretry.Tx, l Label) error

// Create adds a label to a calendar. The calendar row is locked for the
// count and the insert so concurrent creates cannot pass the cap together.
// The name is normalised here (an unusable one answers
// VALIDATION.BODY.FIELD_INVALID with field "name") and the colour, already
// checked as #RRGGBB by the caller, is stored upper-cased.
//
// Store failures are answered as the CALENDAR.LABEL.STORE_*_INTERRUPTED
// refusals, so the error return of other helpers here is not needed.
//
// The caller has already decided the actor may manage the calendar and
// records the change (event CalLabelCreated, audit calendar.label.create,
// resource calendar.label, payload calendarId / labelId / Summary(name)),
// either in afterWrite, atomically with the insert, or after Create returns.
func Create(ctx context.Context, db dbretry.TxBeginner, cq *calendar.Queries, workspaceID, calendarID uint32, rawName, color string, afterWrite AfterWrite) (Label, *apierrors.APIError) {
	name, ok := NormalizeName(rawName)
	if !ok {
		return Label{}, invalidName()
	}
	color = strings.ToUpper(color)
	publicID := types.New()

	var answered *apierrors.APIError
	var created Label
	txErr := dbretry.InTx(ctx, db, "calendarlabels.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		q := cq.WithTx(tx.RawTx())
		if _, err := q.LockCalendarForLabelWrite(ctx, calendar.LockCalendarForLabelWriteParams{ID: calendarID, WorkspaceID: workspaceID}); err != nil {
			answered = apierrors.New(apierrors.CalendarLabelStoreReadInterrupted)
			return err
		}
		count, err := q.CountCalendarLabels(ctx, calendarID)
		if err != nil {
			answered = apierrors.New(apierrors.CalendarLabelStoreReadInterrupted)
			return err
		}
		if count >= MaxPerCalendar {
			answered = apierrors.New(apierrors.CalendarLabelLimitReached)
			return errAnswered
		}
		if _, err := q.CreateCalendarLabel(ctx, calendar.CreateCalendarLabelParams{
			PublicID:    publicID,
			WorkspaceID: workspaceID,
			CalendarID:  calendarID,
			Name:        name,
			Color:       color,
		}); err != nil {
			if apierr.IsDuplicateEntry(err) {
				answered = apierrors.New(apierrors.CalendarLabelNameTaken)
				return errAnswered
			}
			answered = apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
			return err
		}
		row, refusal := Find(ctx, q, calendarID, publicID.String())
		if refusal != nil {
			answered = refusal
			return errAnswered
		}
		created = FromFindRow(row)
		if afterWrite != nil {
			if err := afterWrite(ctx, tx, created); err != nil {
				answered = apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		if answered != nil {
			return Label{}, answered
		}
		return Label{}, apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
	}
	return created, nil
}

// Update renames and/or recolours a label; a nil name or colour is left
// alone. changed is false when the result equals the current label, in
// which case nothing was written and the caller records nothing. A given
// name is normalised as in [Create] and a given colour stored upper-cased.
//
// The caller records a changed update (event CalLabelUpdated, audit
// calendar.label.update, resource calendar.label, payload calendarId /
// labelId / Summary(label.Name)), either in afterWrite (run only when the
// label changed) or after Update returns.
func Update(ctx context.Context, db dbretry.TxBeginner, cq *calendar.Queries, workspaceID, calendarID uint32, labelID string, rawName, rawColor *string, afterWrite AfterWrite) (label Label, changed bool, refusal *apierrors.APIError) {
	var newName *string
	if rawName != nil {
		name, ok := NormalizeName(*rawName)
		if !ok {
			return Label{}, false, invalidName()
		}
		newName = &name
	}

	var answered *apierrors.APIError
	var updated calendar.FindCalendarLabelByPublicIdRow
	txErr := dbretry.InTx(ctx, db, "calendarlabels.Update", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered, changed = nil, false
		q := cq.WithTx(tx.RawTx())
		if _, err := q.LockCalendarForLabelWrite(ctx, calendar.LockCalendarForLabelWriteParams{ID: calendarID, WorkspaceID: workspaceID}); err != nil {
			answered = apierrors.New(apierrors.CalendarLabelStoreReadInterrupted)
			return err
		}
		cur, found := Find(ctx, q, calendarID, labelID)
		if found != nil {
			answered = found
			return errAnswered
		}
		name, color := cur.Name, cur.Color
		if newName != nil {
			name = *newName
		}
		if rawColor != nil {
			color = strings.ToUpper(*rawColor)
		}
		if name == cur.Name && color == cur.Color {
			updated = cur
			return nil
		}
		if err := q.UpdateCalendarLabel(ctx, calendar.UpdateCalendarLabelParams{Name: name, Color: color, ID: cur.ID}); err != nil {
			if apierr.IsDuplicateEntry(err) {
				answered = apierrors.New(apierrors.CalendarLabelNameTaken)
				return errAnswered
			}
			answered = apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
			return err
		}
		changed = true
		var again *apierrors.APIError
		updated, again = Find(ctx, q, calendarID, labelID)
		if again != nil {
			answered = again
			return errAnswered
		}
		if afterWrite != nil {
			if err := afterWrite(ctx, tx, FromFindRow(updated)); err != nil {
				answered = apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		if answered != nil {
			return Label{}, false, answered
		}
		return Label{}, false, apierrors.New(apierrors.CalendarLabelStoreWriteInterrupted)
	}
	return FromFindRow(updated), changed, nil
}

// Delete removes a label and returns what was removed. The links to it go
// with it through the foreign key cascade, so its events fall back to their
// owner's colour. Find, delete and afterWrite run in one transaction; an
// afterWrite error rolls the delete back and is answered as
// CALENDAR.LABEL.STORE_DELETE_INTERRUPTED.
//
// The caller records the removal (event CalLabelDeleted, audit
// calendar.label.delete, resource calendar.label, payload calendarId /
// labelId / Summary(label.Name)), either in afterWrite or after Delete
// returns.
func Delete(ctx context.Context, db dbretry.TxBeginner, cq *calendar.Queries, calendarID uint32, labelID string, afterWrite AfterWrite) (Label, *apierrors.APIError) {
	var answered *apierrors.APIError
	var removed Label
	txErr := dbretry.InTx(ctx, db, "calendarlabels.Delete", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		q := cq.WithTx(tx.RawTx())
		row, refusal := Find(ctx, q, calendarID, labelID)
		if refusal != nil {
			answered = refusal
			return errAnswered
		}
		n, err := q.DeleteCalendarLabel(ctx, calendar.DeleteCalendarLabelParams{ID: row.ID, CalendarID: calendarID})
		if err != nil {
			answered = apierrors.New(apierrors.CalendarLabelStoreDeleteInterrupted)
			return err
		}
		if n == 0 {
			answered = apierrors.New(apierrors.CalendarLabelNotFound)
			return errAnswered
		}
		removed = FromFindRow(row)
		if afterWrite != nil {
			if err := afterWrite(ctx, tx, removed); err != nil {
				answered = apierrors.New(apierrors.CalendarLabelStoreDeleteInterrupted)
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		if answered != nil {
			return Label{}, answered
		}
		return Label{}, apierrors.New(apierrors.CalendarLabelStoreDeleteInterrupted)
	}
	return removed, nil
}

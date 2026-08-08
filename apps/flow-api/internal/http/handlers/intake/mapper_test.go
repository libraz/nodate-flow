package intake

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestEveryRecordFieldIsFilledFromAFullRow walks the DTO the intake
// endpoints return and requires every field to carry a value when the
// source row has one.
//
// The DTO used to advertise a `taskId` that no mapper ever assigned and
// no query could supply, so every response omitted it and every
// generated client typed a field that was never there. This test is what
// stops a field being declared ahead of the data again: a new one has to
// be filled by the mapper, from a column the query actually selects.
func TestEveryRecordFieldIsFilledFromAFullRow(t *testing.T) {
	t.Parallel()

	pub := types.PublicID(uuid.Must(uuid.NewV7()))
	now := time.Now()
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

	got := map[string]Record{
		"list": mapListRow(generated.ListIntakeItemsForWorkspaceRow{
			PublicID:             pub,
			Title:                "title",
			Body:                 str("body"),
			TriageStatus:         generated.IntakeItemsTriageStatusPending,
			SnoozeUntil:          sql.NullTime{Time: now, Valid: true},
			AiScore:              str("0.5"),
			AiReasoning:          str("reasoning"),
			TriagedByPublicID:    pub,
			TriagedByDisplayName: str("Someone"),
			CreatedAt:            now,
		}),
		"keyset": mapKeysetListRow(generated.ListIntakeItemsForWorkspaceKeysetRow{
			PublicID:             pub,
			Title:                "title",
			Body:                 str("body"),
			TriageStatus:         generated.IntakeItemsTriageStatusPending,
			SnoozeUntil:          sql.NullTime{Time: now, Valid: true},
			AiScore:              str("0.5"),
			AiReasoning:          str("reasoning"),
			TriagedByPublicID:    pub,
			TriagedByDisplayName: str("Someone"),
			CreatedAt:            now,
		}),
	}

	for name, record := range got {
		v := reflect.ValueOf(record)
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			if v.Field(i).IsZero() {
				t.Errorf("%s mapper left Record.%s empty even though the row carried every column it could — either fill it or drop it from the DTO", name, typ.Field(i).Name)
			}
		}
	}
}

// TestFindRowFillsWhatTheQuerySelects is the single-item counterpart.
// FindIntakeItemByPublicId does not join users, so the two triage-actor
// fields are legitimately absent from it; everything else must be there.
func TestFindRowFillsWhatTheQuerySelects(t *testing.T) {
	t.Parallel()

	pub := types.PublicID(uuid.Must(uuid.NewV7()))
	now := time.Now()
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

	record := mapFindRow(generated.FindIntakeItemByPublicIdRow{
		PublicID:     pub,
		Title:        "title",
		Body:         str("body"),
		TriageStatus: generated.IntakeItemsTriageStatusPending,
		SnoozeUntil:  sql.NullTime{Time: now, Valid: true},
		AiScore:      str("0.5"),
		AiReasoning:  str("reasoning"),
		CreatedAt:    now,
	})

	unjoined := map[string]bool{
		"TriagedByUserID":      true,
		"TriagedByDisplayName": true,
	}
	v := reflect.ValueOf(record)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if unjoined[name] {
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("mapFindRow left Record.%s empty — either fill it or drop it from the DTO", name)
		}
	}
}

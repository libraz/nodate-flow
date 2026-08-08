package ai

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// upsertFieldsSetFromContext are the columns the PATCH handler fills in
// itself rather than carrying over from the row it read: the workspace is
// the path parameter, and the modifying user is the caller.
var upsertFieldsSetFromContext = map[string]bool{
	"WorkspaceID":      true,
	"ModifiedByUserID": true,
}

// TestAiSettingsUpsertCarriesEveryColumn walks UpsertAiSettingsParams by
// field name and requires each one to arrive from the row that was read.
//
// UpsertAiSettings rewrites every column it takes, so a field the mapper
// forgets is not left alone — it is zeroed. judge_instructions is the one
// that was actually being erased, but the failure mode belongs to the
// shape of the query rather than to that column, so the check is over the
// whole struct: adding a column to the upsert without carrying it fails
// here instead of quietly deleting operator data in production.
func TestAiSettingsUpsertCarriesEveryColumn(t *testing.T) {
	t.Parallel()

	row := filledSettingsRow(t)
	params := aiSettingsUpsertFromRow(99, row)

	rowVal := reflect.ValueOf(row)
	paramsVal := reflect.ValueOf(params)
	paramsType := paramsVal.Type()

	checked := 0
	for i := range paramsType.NumField() {
		name := paramsType.Field(i).Name
		if upsertFieldsSetFromContext[name] {
			continue
		}
		source := rowVal.FieldByName(name)
		if !source.IsValid() {
			t.Errorf("UpsertAiSettingsParams.%s has no counterpart on GetAiSettingsRow; the mapper cannot preserve it", name)
			continue
		}
		got := paramsVal.Field(i).Interface()
		want := source.Interface()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("upsert %s = %#v, want %#v carried from the existing row; the upsert rewrites this column, so dropping it deletes the stored value", name, got, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no fields compared; the reflection walk is not exercising the mapper")
	}
	if params.WorkspaceID != 99 {
		t.Errorf("upsert WorkspaceID = %d, want the workspace the handler is acting on", params.WorkspaceID)
	}
}

// TestAiSettingsUpsertPreservesJudgeInstructions states the concrete case
// behind the walk above: toggling the auto-action executor must not clear
// the free-form judge instructions, which have no UI to restore them and
// leave no audit trail when they vanish.
func TestAiSettingsUpsertPreservesJudgeInstructions(t *testing.T) {
	t.Parallel()

	instructions := sql.NullString{String: "Escalate anything mentioning an outage.", Valid: true}
	params := aiSettingsUpsertFromRow(1, generated.GetAiSettingsRow{
		EmbedModel:        defaultEmbedModel,
		JudgeInstructions: instructions,
	})

	if params.JudgeInstructions != instructions {
		t.Fatalf("JudgeInstructions = %#v, want %#v", params.JudgeInstructions, instructions)
	}
}

// TestAiSettingsUpsertDefaultsLeaveJudgeInstructionsNull covers the
// first-write path: a workspace with no ai_settings row has no
// instructions to preserve, and the default payload must write NULL
// rather than an empty string that would later read back as "configured".
func TestAiSettingsUpsertDefaultsLeaveJudgeInstructionsNull(t *testing.T) {
	t.Parallel()

	params := aiSettingsUpsertDefaults(1)
	if params.JudgeInstructions.Valid {
		t.Fatalf("JudgeInstructions = %#v, want NULL for a workspace that has never written settings", params.JudgeInstructions)
	}
	if params.EmbedModel == "" {
		t.Error("EmbedModel is empty; the upsert would write an unusable embed model for a first-time workspace")
	}
}

// filledSettingsRow returns a GetAiSettingsRow whose every supported field
// holds a distinct non-zero value, so a dropped field shows up as a zero
// value rather than coincidentally matching.
func filledSettingsRow(t *testing.T) generated.GetAiSettingsRow {
	t.Helper()

	var row generated.GetAiSettingsRow
	v := reflect.ValueOf(&row).Elem()
	typ := v.Type()
	for i := range typ.NumField() {
		field := v.Field(i)
		switch field.Interface().(type) {
		case string:
			field.SetString(fmt.Sprintf("value-%d", i))
		case uint32:
			field.SetUint(uint64(i) + 1)
		case bool:
			field.SetBool(true)
		case sql.NullString:
			field.Set(reflect.ValueOf(sql.NullString{String: fmt.Sprintf("null-string-%d", i), Valid: true}))
		}
	}
	return row
}

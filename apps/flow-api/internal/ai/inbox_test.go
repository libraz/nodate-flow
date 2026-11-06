package ai

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/inboxsnapshot"
)

// TestInboxTriagePromptOmitsUnreadableTitles holds the triage prompt to
// the reader's visibility: the row stays, so the item can still be
// scored, but the title of a task the reader may not open never reaches
// the provider or the cached proposal served back to them.
func TestInboxTriagePromptOmitsUnreadableTitles(t *testing.T) {
	t.Parallel()

	readableTaskID, privateTaskID := types.New(), types.New()
	rows := []generated.ListInboxRow{
		{
			PublicID:     types.New(),
			TaskTitle:    sql.NullString{String: "Readable task title", Valid: true},
			TaskPublicID: sql.NullString{String: string(readableTaskID[:]), Valid: true},
			TaskVisible:  sql.NullBool{Bool: true, Valid: true},
		},
		{
			PublicID:     types.New(),
			TaskTitle:    sql.NullString{String: "Private task title", Valid: true},
			TaskPublicID: sql.NullString{String: string(privateTaskID[:]), Valid: true},
			TaskVisible:  sql.NullBool{Bool: false, Valid: true},
		},
	}

	prompt, _, _, _, _, err := captureInboxTriageInput(rows)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(prompt, "Private task title") {
		t.Errorf("prompt carries the title of a task the reader may not read: %s", prompt)
	}
	if !strings.Contains(prompt, "Readable task title") {
		t.Errorf("prompt dropped a readable title, so this test proves nothing: %s", prompt)
	}
	if !strings.Contains(prompt, rows[1].PublicID.String()) {
		t.Errorf("the unreadable item must still be listed for scoring: %s", prompt)
	}
}

func TestInboxTriageInputIsBoundedAndCarriesOnlyReadableTaskResources(t *testing.T) {
	t.Parallel()

	readableTaskID := types.New()
	emptyTitleTaskID := types.New()
	privateTaskID := types.New()
	rows := []generated.ListInboxRow{
		{
			PublicID: types.New(),
			Source:   generated.SignalsSource("manual"),
			Kind:     "manual",
			PayloadJson: json.RawMessage(`{"title":"` + strings.Repeat("題", 600) + `","body":"` +
				strings.Repeat("文", 1200) + `","sourceUrl":"https://private.example.test/item"}`),
			TaskPublicID: typesNullString(string(readableTaskID[:])),
			TaskTitle:    typesNullString("Readable linked task"),
			TaskVisible:  sql.NullBool{Bool: true, Valid: true},
			ReceivedAt:   time.UnixMilli(1_700_000_000_100),
			UpdatedAt:    sql.NullTime{Time: time.UnixMilli(1_700_000_001_200), Valid: true},
		},
		{
			PublicID:     types.New(),
			Source:       generated.SignalsSource("manual"),
			Kind:         "manual",
			PayloadJson:  json.RawMessage(`{"title":"Private signal title","body":"Private signal body"}`),
			TaskPublicID: typesNullString(string(privateTaskID[:])),
			TaskTitle:    typesNullString("Unreadable linked task"),
			TaskVisible:  sql.NullBool{Bool: false, Valid: true},
			ReceivedAt:   time.UnixMilli(1_700_000_000_200),
			UpdatedAt:    sql.NullTime{Time: time.UnixMilli(1_700_000_001_300), Valid: true},
		},
		{
			PublicID:    types.New(),
			Source:      generated.SignalsSource("manual"),
			Kind:        "manual",
			PayloadJson: json.RawMessage(`{"title":"Task id missing"}`),
			TaskTitle:   typesNullString("Task without a public reference"),
			TaskVisible: sql.NullBool{Bool: true, Valid: true},
			ReceivedAt:  time.UnixMilli(1_700_000_000_300),
			UpdatedAt:   sql.NullTime{Time: time.UnixMilli(1_700_000_001_400), Valid: true},
		},
		{
			PublicID:     types.New(),
			Source:       generated.SignalsSource("manual"),
			Kind:         "manual",
			PayloadJson:  json.RawMessage(`{"title":"No task title"}`),
			TaskPublicID: typesNullString(string(emptyTitleTaskID[:])),
			TaskVisible:  sql.NullBool{Bool: true, Valid: true},
			ReceivedAt:   time.UnixMilli(1_700_000_000_400),
			UpdatedAt:    sql.NullTime{Time: time.UnixMilli(1_700_000_001_500), Valid: true},
		},
	}

	prompt, versions, resources, taskRefs, openableInboxItems, err := captureInboxTriageInput(rows)
	if err != nil {
		t.Fatalf("capture input: %v", err)
	}
	var promptItems []inboxsnapshot.PromptItem
	if err := json.Unmarshal([]byte(prompt), &promptItems); err != nil {
		t.Fatalf("decode prompt: %v", err)
	}
	var actionItems []struct {
		InboxItemID    string   `json:"inboxItemId"`
		AllowedActions []string `json:"allowedActions"`
	}
	if err := json.Unmarshal([]byte(prompt), &actionItems); err != nil {
		t.Fatalf("decode allowed inbox actions: %v", err)
	}
	if len(promptItems) != 4 {
		t.Fatalf("prompt item count = %d, want 4", len(promptItems))
	}
	if len(actionItems) != 4 {
		t.Fatalf("prompt action item count = %d, want 4", len(actionItems))
	}
	wantActions := [][]string{{"archive", "snooze", "open"}, {"archive", "snooze"}, {"archive", "snooze"}, {"archive", "snooze", "open"}}
	for i, want := range wantActions {
		if actionItems[i].InboxItemID != rows[i].PublicID.String() || !reflect.DeepEqual(actionItems[i].AllowedActions, want) {
			t.Fatalf("prompt actions for inbox row %d = %+v, want item=%s actions=%v", i, actionItems[i], rows[i].PublicID, want)
		}
	}
	if got := len([]rune(promptItems[0].SourceTitle)); got != 500 {
		t.Fatalf("source title rune count = %d, want 500", got)
	}
	if got := len([]rune(promptItems[0].SourceBody)); got != 1_000 {
		t.Fatalf("source body rune count = %d, want 1000", got)
	}
	if promptItems[0].TaskTitle != "Readable linked task" {
		t.Fatalf("readable task title missing from prompt: %+v", promptItems[0])
	}
	if promptItems[1].TaskTitle != "" {
		t.Fatalf("unreadable task title reached prompt: %+v", promptItems[1])
	}
	if promptItems[2].TaskTitle != "" {
		t.Fatalf("task title without a public reference reached prompt: %+v", promptItems[2])
	}
	if promptItems[3].TaskTitle != "" {
		t.Fatalf("empty task title unexpectedly reached prompt: %+v", promptItems[3])
	}
	if strings.Contains(prompt, "private.example.test") || strings.Contains(prompt, "Unreadable linked task") || strings.Contains(prompt, privateTaskID.String()) || strings.Contains(prompt, "Task without a public reference") {
		t.Fatalf("prompt contains a URL or unreadable task reference: %s", prompt)
	}
	if len(versions) != 4 || versions[rows[0].PublicID.String()] == "" || versions[rows[1].PublicID.String()] == "" || versions[rows[2].PublicID.String()] == "" || versions[rows[3].PublicID.String()] == "" {
		t.Fatalf("source version map does not cover all input items: %+v", versions)
	}
	if len(resources) != 1 || resources[0] != TaskInputResource(readableTaskID.String()) {
		t.Fatalf("invocation resources = %+v, want only readable quoted task", resources)
	}
	if len(taskRefs) != 2 {
		t.Fatalf("suggestion task refs = %+v, want both readable linked tasks", taskRefs)
	}
	if len(openableInboxItems) != 2 {
		t.Fatalf("openable inbox items = %+v, want both readable linked rows", openableInboxItems)
	}
	if _, ok := openableInboxItems[rows[0].PublicID.String()]; !ok {
		t.Fatalf("readable linked inbox item is not openable: %+v", openableInboxItems)
	}
	if _, ok := openableInboxItems[rows[3].PublicID.String()]; !ok {
		t.Fatalf("readable linked task without a title is still openable: %+v", openableInboxItems)
	}
	for _, unreadable := range []int{1, 2} {
		if _, ok := openableInboxItems[rows[unreadable].PublicID.String()]; ok {
			t.Fatalf("unreadable or missing task link is openable: row=%d map=%+v", unreadable, openableInboxItems)
		}
	}
	for _, expected := range []types.PublicID{readableTaskID, emptyTitleTaskID} {
		found := false
		for _, taskID := range taskRefs {
			if taskID == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("suggestion provenance omits readable linked task %s: %+v", expected, taskRefs)
		}
	}
}

func TestInboxTriageRejectsMalformedReadableTaskReference(t *testing.T) {
	row := generated.ListInboxRow{
		PublicID:     types.New(),
		Source:       generated.SignalsSource("manual"),
		Kind:         "manual",
		TaskPublicID: typesNullString("not-a-binary-uuid"),
		TaskTitle:    typesNullString("Must not be sent"),
		TaskVisible:  sql.NullBool{Bool: true, Valid: true},
		ReceivedAt:   time.UnixMilli(1_700_000_000_100),
	}
	prompt, _, _, _, _, err := captureInboxTriageInput([]generated.ListInboxRow{row})
	if err == nil {
		t.Fatal("capture succeeded with a malformed readable task reference")
	}
	if prompt != "" {
		t.Fatalf("malformed task reference did not fail closed: %q", prompt)
	}
}

func typesNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

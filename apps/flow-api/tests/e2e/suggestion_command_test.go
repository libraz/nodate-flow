package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/inboxsnapshot"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type commandSuggestion struct {
	EventID             string  `json:"eventId"`
	ProposalID          string  `json:"proposalId"`
	InboxItemID         string  `json:"inboxItemId"`
	RecommendedAction   string  `json:"recommendedAction"`
	Reasoning           string  `json:"reasoning"`
	Score               float32 `json:"score"`
	Revision            int     `json:"revision"`
	DecisionExpiresAt   int64   `json:"decisionExpiresAt"`
	SourceVersionStatus string  `json:"sourceVersionStatus"`
}

type suggestionCommandResult struct {
	OperationID string `json:"operationId"`
	ProposalID  string `json:"proposalId"`
	Revision    int    `json:"revision"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	InboxItemID string `json:"inboxItemId"`
	TaskID      string `json:"taskId"`
	SnoozeUntil int64  `json:"snoozeUntil"`
	Replayed    bool   `json:"replayed"`
}

func seedCommandSuggestions(t *testing.T, tt *helpers.TestTenant, openTaskPublicID string) []commandSuggestion {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	actorID := helpers.ResolveUserInternalID(t, testDB, tt.UserPublicID)
	// Open is only listed and executable with a readable linked task.
	if openTaskPublicID == "" {
		openTaskPublicID = createTaskWithVisibility(t, tt, "Seeded open suggestion target", "public")
	}
	var openTaskInternalID uint32
	var openTaskPublicIDValue types.PublicID
	if openTaskPublicID != "" {
		_, openTaskInternalID = helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, openTaskPublicID)
		require.NoError(t, testDB.QueryRow(
			`SELECT public_id FROM tasks WHERE workspace_id = ? AND id = ?`,
			workspaceID, openTaskInternalID,
		).Scan(&openTaskPublicIDValue))
	}
	batchPublicID := uuid.Must(uuid.NewV7())
	inputCount := 0
	if openTaskInternalID != 0 {
		inputCount = 1
	}
	batchResult, err := testDB.Exec(
		`INSERT INTO ai_suggestion_batches (public_id, workspace_id, user_id, input_count, provenance_valid)
		 VALUES (?, ?, ?, ?, TRUE)`,
		batchPublicID[:], workspaceID, actorID, inputCount)
	require.NoError(t, err)
	batchID, err := batchResult.LastInsertId()
	require.NoError(t, err)
	if openTaskInternalID != 0 {
		_, err := testDB.Exec(
			`INSERT INTO ai_suggestion_batches_inputs (batch_id, task_public_id) VALUES (?, ?)`,
			batchID, openTaskPublicIDValue[:])
		require.NoError(t, err)
	}

	// Give each test its own signal and proposal IDs. The shared mock fixture
	// uses fixed IDs, which collide when parallel tests exercise this contract.
	for _, proposal := range []struct {
		action    string
		reasoning string
	}{
		{action: "open", reasoning: "Deterministic open recommendation."},
		{action: "snooze", reasoning: "Deterministic snooze recommendation."},
		{action: "archive", reasoning: "Deterministic archive recommendation."},
	} {
		inboxItemID := uuid.Must(uuid.NewV7())
		var taskID any
		var taskInternalID uint32
		subjectType := "workspace"
		var subjectID any
		if proposal.action == "open" && openTaskInternalID != 0 {
			taskID = openTaskInternalID
			taskInternalID = openTaskInternalID
			subjectType = "task"
			subjectID = openTaskInternalID
		}
		_, err := testDB.Exec(
			`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at, subject_type, subject_id)
			 VALUES (?, ?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3), ?, ?)`,
			inboxItemID[:], workspaceID, taskID, subjectType, subjectID)
		require.NoError(t, err)
		var receivedAt, updatedAt time.Time
		require.NoError(t, testDB.QueryRow(
			`SELECT received_at, updated_at FROM signals WHERE workspace_id = ? AND public_id = ?`,
			workspaceID, inboxItemID[:],
		).Scan(&receivedAt, &updatedAt))
		fields := inboxsnapshot.Fields{
			InboxItemID: inboxItemID.String(),
			Source:      "manual",
			Kind:        "manual",
			Payload:     []byte(`{}`),
			ReceivedAt:  receivedAt,
			UpdatedAt:   updatedAt,
		}
		if taskInternalID != 0 {
			var taskTitle string
			require.NoError(t, testDB.QueryRow(
				`SELECT title FROM tasks WHERE workspace_id = ? AND id = ?`,
				workspaceID, taskInternalID,
			).Scan(&taskTitle))
			fields.TaskPublicID = openTaskPublicIDValue.String()
			fields.TaskTitle = taskTitle
			fields.TaskReadable = true
		}
		sourceVersion := inboxsnapshot.Capture(fields).VersionHash()

		proposalEventID := uuid.Must(uuid.NewV7())
		_, err = testDB.Exec(
			`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
			 VALUES (?, ?, ?, 'ai.suggestion.proposed',
			         JSON_OBJECT('inbox_item_id', ?, 'score', 0.5, 'action', ?, 'reasoning', ?, 'source_version', ?, 'suggestion_batch_id', ?), NOW(3))`,
			proposalEventID[:], workspaceID, actorID, inboxItemID.String(), proposal.action, proposal.reasoning, sourceVersion, batchPublicID.String())
		require.NoError(t, err)
	}

	var list struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list)
	require.Len(t, list.Suggestions, 3)
	return list.Suggestions
}

func TestAiSuggestionListExposesProposalIdentityAndRevision(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestions := seedCommandSuggestions(t, tt, "")
	var rawList struct {
		Suggestions []map[string]json.RawMessage `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &rawList)
	require.Len(t, rawList.Suggestions, 3)
	for _, rawSuggestion := range rawList.Suggestions {
		proposalID, ok := rawSuggestion["proposalId"]
		require.True(t, ok, "suggestions expose proposalId")
		var proposal string
		require.NoError(t, json.Unmarshal(proposalID, &proposal))
		require.NotEmpty(t, proposal)
		revisionJSON, ok := rawSuggestion["revision"]
		require.True(t, ok, "suggestions expose revision")
		var revision int
		require.NoError(t, json.Unmarshal(revisionJSON, &revision))
		require.GreaterOrEqual(t, revision, 0)
		statusJSON, ok := rawSuggestion["sourceVersionStatus"]
		require.True(t, ok, "suggestions expose sourceVersionStatus")
		var sourceVersionStatus string
		require.NoError(t, json.Unmarshal(statusJSON, &sourceVersionStatus))
		require.Equal(t, "captured", sourceVersionStatus)
	}
	for _, suggestion := range suggestions {
		require.NotEmpty(t, suggestion.EventID)
		require.GreaterOrEqual(t, suggestion.Revision, 0, "new proposals start at a non-negative revision")
	}
}

func suggestionForAction(t *testing.T, suggestions []commandSuggestion, action string) commandSuggestion {
	t.Helper()
	for _, suggestion := range suggestions {
		if suggestion.RecommendedAction == action {
			return suggestion
		}
	}
	t.Fatalf("missing %q suggestion in %+v", action, suggestions)
	return commandSuggestion{}
}

func commandRequest(s commandSuggestion, operationKey, action string) map[string]any {
	proposalID := s.ProposalID
	if proposalID == "" {
		// Keep the execute route test exercising its contract while the list
		// mapper assertion above independently proves whether proposalId exists.
		proposalID = s.EventID
	}
	return map[string]any{
		"operationKey":     operationKey,
		"proposalId":       proposalID,
		"expectedRevision": s.Revision,
		"action":           action,
	}
}

func executeSuggestion(t *testing.T, tt *helpers.TestTenant, s commandSuggestion, operationKey, action string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+s.InboxItemID+"/execute",
		tt.AccessToken, commandRequest(s, operationKey, action))
}

func countSuggestionEvent(t *testing.T, tt *helpers.TestTenant, eventType, inboxItemID, proposalID string) int64 {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	var count int64
	err := testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM events
		 WHERE workspace_id = ?
		   AND type = ?
		   AND COALESCE(
		     JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.inboxItemId')),
		     JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.inbox_item_id')),
		     JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.proposalId')),
		     JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.proposal_id'))
		   ) IN (?, ?)`,
		workspaceID, eventType, inboxItemID, proposalID,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func countSuggestionReceipt(t *testing.T, tt *helpers.TestTenant, operationKey string) int64 {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	var count int64
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM operation_receipts WHERE workspace_id = ? AND operation_key = ?`,
		workspaceID, operationKey,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func installSuggestionAppliedFailure(t *testing.T, proposalID string) func() {
	t.Helper()
	trigger := "trg_test_suggestion_applied_" + randomHex(5)
	statement := fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON events FOR EACH ROW
BEGIN
	IF NEW.type = 'ai.suggestion.applied' AND (
		JSON_UNQUOTE(JSON_EXTRACT(NEW.payload_json, '$.proposalId')) = '%s' OR
		JSON_UNQUOTE(JSON_EXTRACT(NEW.payload_json, '$.proposal_id')) = '%s'
	) THEN
		SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced suggestion applied append failure';
	END IF;
END`, trigger, proposalID, proposalID)
	_, err := testDB.Exec(statement)
	require.NoError(t, err)
	active := true
	drop := func() {
		if !active {
			return
		}
		_, err := testDB.Exec("DROP TRIGGER IF EXISTS " + trigger)
		if err != nil {
			t.Errorf("drop test trigger %s: %v", trigger, err)
		}
		active = false
	}
	t.Cleanup(drop)
	return drop
}

func assertSignalEnabled(t *testing.T, tt *helpers.TestTenant, inboxItemID string, enabled bool) {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	var got bool
	err := testDB.QueryRow(
		`SELECT enabled FROM signals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, inboxItemID,
	).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, enabled, got)
}

func TestAiSuggestionExecuteArchivesAtomicallyAndReplaysAfterSignalDisable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestions := seedCommandSuggestions(t, tt, "")
	suggestion := suggestionForAction(t, suggestions, "archive")
	operationKey := "archive-replay-" + randomHex(8)

	status, raw := executeSuggestion(t, tt, suggestion, operationKey, "archive")
	require.Equalf(t, http.StatusOK, status, "execute body=%s", string(raw))
	var first suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &first))
	require.NotEmpty(t, first.OperationID)
	require.Equal(t, suggestion.ProposalID, first.ProposalID)
	require.Equal(t, "executed", first.Status)
	require.Equal(t, "archive", first.Action)
	require.False(t, first.Replayed)
	assertSignalEnabled(t, tt, suggestion.InboxItemID, false)
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "signal.archived", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionReceipt(t, tt, operationKey))

	// A receipt is useful after a response is lost only if a matching replay
	// remains authorized after the signal leaves the ordinary inbox view.
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	_, err := testDB.Exec(`UPDATE signals SET enabled = FALSE WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`, workspaceID, suggestion.InboxItemID)
	require.NoError(t, err)

	status, raw = executeSuggestion(t, tt, suggestion, operationKey, "archive")
	require.Equalf(t, http.StatusOK, status, "matching replay body=%s", string(raw))
	var replay suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &replay))
	require.True(t, replay.Replayed)
	require.Equal(t, first.OperationID, replay.OperationID)
	require.Equal(t, first.ProposalID, replay.ProposalID)
	require.Equal(t, first.Revision, replay.Revision)
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "signal.archived", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))

	conflicting := commandRequest(suggestion, operationKey, "snooze")
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+suggestion.InboxItemID+"/execute",
		tt.AccessToken, conflicting)
	require.Equalf(t, http.StatusConflict, status, "changed request body=%s", string(raw))
	require.Equal(t, "AI.OPERATION.KEY_CONFLICT", problemType(t, raw))

	status, raw = executeSuggestion(t, tt, suggestion, "second-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusConflict, status, "resolved proposal body=%s", string(raw))
	require.Equal(t, "AI.SUGGESTION.ALREADY_RESOLVED", problemType(t, raw))
}

func TestAiSuggestionExecuteSnoozesAtomicallyAndReplays(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestions := seedCommandSuggestions(t, tt, "")
	suggestion := suggestionForAction(t, suggestions, "snooze")
	operationKey := "snooze-replay-" + randomHex(8)
	snoozeUntil := time.Now().Add(time.Hour).Unix()
	request := commandRequest(suggestion, operationKey, "snooze")
	request["snoozeUntil"] = snoozeUntil
	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/suggestions/" + suggestion.InboxItemID + "/execute"

	status, raw := doJSONStatus(t, http.MethodPost, url, tt.AccessToken, request)
	require.Equalf(t, http.StatusOK, status, "snooze command body=%s", string(raw))
	var first suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &first))
	require.Equal(t, "executed", first.Status)
	require.Equal(t, "snooze", first.Action)
	require.Equal(t, snoozeUntil, first.SnoozeUntil)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	var receivedAt time.Time
	require.NoError(t, testDB.QueryRow(
		`SELECT received_at FROM signals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, suggestion.InboxItemID,
	).Scan(&receivedAt))
	require.Equal(t, time.Unix(snoozeUntil, 0).UTC(), receivedAt.UTC())
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "signal.snoozed", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionReceipt(t, tt, operationKey))
	assertSignalEnabled(t, tt, suggestion.InboxItemID, true)
	var afterSnoozeInbox struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &afterSnoozeInbox)
	require.NotContains(t, inboxItemIDs(afterSnoozeInbox.Items), suggestion.InboxItemID,
		"an applied snooze must leave the fresh Inbox view while retaining its source row")

	status, raw = doJSONStatus(t, http.MethodPost, url, tt.AccessToken, request)
	require.Equalf(t, http.StatusOK, status, "snooze replay body=%s", string(raw))
	var replay suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &replay))
	require.True(t, replay.Replayed)
	require.Equal(t, first.OperationID, replay.OperationID)
	require.Equal(t, first.SnoozeUntil, replay.SnoozeUntil)
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "signal.snoozed", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &afterSnoozeInbox)
	require.NotContains(t, inboxItemIDs(afterSnoozeInbox.Items), suggestion.InboxItemID,
		"replaying the applied operation must not bring the snoozed row back into Inbox")
}

func TestAiSuggestionExecuteRollsBackAppendFailureAndAllowsRetry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestions := seedCommandSuggestions(t, tt, "")
	suggestion := suggestionForAction(t, suggestions, "archive")
	operationKey := "rollback-retry-" + randomHex(8)
	dropTrigger := installSuggestionAppliedFailure(t, suggestion.ProposalID)

	status, raw := executeSuggestion(t, tt, suggestion, operationKey, "archive")
	require.GreaterOrEqualf(t, status, http.StatusInternalServerError, "failed append body=%s", string(raw))
	assertSignalEnabled(t, tt, suggestion.InboxItemID, true)
	require.Zero(t, countSuggestionEvent(t, tt, "signal.archived", suggestion.InboxItemID, suggestion.ProposalID))
	require.Zero(t, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
	require.Zero(t, countSuggestionReceipt(t, tt, operationKey))

	dropTrigger()
	status, raw = executeSuggestion(t, tt, suggestion, operationKey, "archive")
	require.Equalf(t, http.StatusOK, status, "retry body=%s", string(raw))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "signal.archived", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
	require.EqualValues(t, 1, countSuggestionReceipt(t, tt, operationKey))
}

func TestAiSuggestionDecisionEditPersistsAndRevisionGuardsExecution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestions := seedCommandSuggestions(t, tt, "")
	suggestion := suggestionForAction(t, suggestions, "open")
	decisionURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/suggestions/" + suggestion.InboxItemID + "/decision"
	decisionBody := map[string]any{
		"proposalId":       suggestion.ProposalID,
		"expectedRevision": suggestion.Revision,
		"state":            "edited",
		"action":           "archive",
		"reasoning":        "Edited by the user and persisted.",
	}
	status, raw := doJSONStatus(t, http.MethodPatch, decisionURL, tt.AccessToken, decisionBody)
	require.Equalf(t, http.StatusOK, status, "decision body=%s", string(raw))
	var saved struct {
		ProposalID        string `json:"proposalId"`
		Revision          int    `json:"revision"`
		State             string `json:"state"`
		Action            string `json:"action"`
		Reasoning         string `json:"reasoning"`
		DecisionExpiresAt int64  `json:"decisionExpiresAt"`
	}
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Equal(t, suggestion.ProposalID, saved.ProposalID)
	require.Equal(t, suggestion.Revision+1, saved.Revision)
	require.Equal(t, "edited", saved.State)
	require.Equal(t, "archive", saved.Action)
	require.Equal(t, "Edited by the user and persisted.", saved.Reasoning)
	require.Greater(t, saved.DecisionExpiresAt, time.Now().Unix())

	var refetched struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &refetched)
	require.Len(t, refetched.Suggestions, 3)
	var updated commandSuggestion
	foundUpdatedProposal := false
	for _, candidate := range refetched.Suggestions {
		if candidate.ProposalID == suggestion.ProposalID {
			updated = candidate
			foundUpdatedProposal = true
			break
		}
	}
	require.True(t, foundUpdatedProposal, "refetch should retain the edited proposal identity")
	require.Equal(t, suggestion.InboxItemID, updated.InboxItemID)
	require.Equal(t, "archive", updated.RecommendedAction)
	require.Equal(t, suggestion.ProposalID, updated.ProposalID)
	require.Equal(t, saved.Revision, updated.Revision)
	require.Equal(t, saved.DecisionExpiresAt, updated.DecisionExpiresAt)
	require.Equal(t, saved.Reasoning, updated.Reasoning)

	status, raw = doJSONStatus(t, http.MethodPatch, decisionURL, tt.AccessToken, decisionBody)
	require.Equalf(t, http.StatusOK, status, "same decision retry body=%s", string(raw))
	var replayedDecision struct {
		Revision          int    `json:"revision"`
		DecisionExpiresAt int64  `json:"decisionExpiresAt"`
		Reasoning         string `json:"reasoning"`
	}
	require.NoError(t, json.Unmarshal(raw, &replayedDecision))
	require.Equal(t, saved.Revision, replayedDecision.Revision)
	require.Equal(t, saved.DecisionExpiresAt, replayedDecision.DecisionExpiresAt)
	require.Equal(t, saved.Reasoning, replayedDecision.Reasoning)
	require.EqualValues(t, 1, countSuggestionEvent(t, tt, "ai.suggestion.edited", suggestion.InboxItemID, suggestion.ProposalID))

	decisionBody["reasoning"] = "A different edit with the stale revision."
	status, raw = doJSONStatus(t, http.MethodPatch, decisionURL, tt.AccessToken, decisionBody)
	require.Equalf(t, http.StatusConflict, status, "changed stale revision body=%s", string(raw))
	require.Equal(t, "AI.SUGGESTION.EVIDENCE_CHANGED", problemType(t, raw))

	request := commandRequest(updated, "edited-command-"+randomHex(8), "archive")
	request["reasoning"] = updated.Reasoning
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions/"+updated.InboxItemID+"/execute",
		tt.AccessToken, request)
	require.Equalf(t, http.StatusOK, status, "edited execution body=%s", string(raw))
	var executed suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &executed))
	require.Equal(t, saved.Revision, executed.Revision)
	require.Equal(t, "archive", executed.Action)
}

func TestAiSuggestionOpenReturnsNavigationTargetWithoutAppliedOutcome(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Task for open suggestion"}, &task)
	require.NotEmpty(t, task.ID)

	suggestions := seedCommandSuggestions(t, tt, task.ID)
	suggestion := suggestionForAction(t, suggestions, "open")
	status, raw := executeSuggestion(t, tt, suggestion, "open-"+randomHex(8), "open")
	require.Equalf(t, http.StatusOK, status, "open command body=%s", string(raw))
	var result suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "navigation_ready", result.Status)
	require.Equal(t, task.ID, result.TaskID)
	require.Zero(t, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))

	var after struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &after)
	require.Len(t, after.Suggestions, 3, "navigation readiness does not resolve or count as an applied effect")
}

func TestAiSuggestionRejectsSourceChangedWhileProviderWasBlocked(t *testing.T) {
	bootstrap(t)
	if aiMockEnabled() {
		t.Skip("this test needs a per-workspace provider that can be held while its source changes")
	}
	t.Parallel()

	tt := newTenant(t)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	inboxItemID := uuid.Must(uuid.NewV7())
	initialTitle := "Source title before the provider call"
	initialBody := "Source body before the provider call"
	_, err := testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type, subject_id)
		 VALUES (?, ?, 'manual', 'manual', JSON_OBJECT('title', ?, 'body', ?), NOW(3), 'workspace', NULL)`,
		inboxItemID[:], workspaceID, initialTitle, initialBody)
	require.NoError(t, err)

	providerStarted := make(chan struct{}, 1)
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseProvider) }) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerStarted <- struct{}{}
		select {
		case <-releaseProvider:
		case <-r.Context().Done():
			return
		}
		content := fmt.Sprintf(
			`[{"inboxItemId":%q,"score":0.9,"recommendedAction":"archive","reasoning":"Archive the old source."}]`,
			inboxItemID.String(),
		)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`,
			content)
	}))
	t.Cleanup(upstream.Close)
	registerOpenAICompatProvider(t, tt, upstream.URL)

	type response struct {
		status int
		body   []byte
		err    error
	}
	triageResponse := make(chan response, 1)
	triageURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/inbox/triage"
	go func() {
		status, body, requestErr := sendJSONStatus(http.MethodPost, triageURL, tt.AccessToken, map[string]any{"limit": 10})
		triageResponse <- response{status: status, body: body, err: requestErr}
	}()

	select {
	case <-providerStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("triage provider did not start")
	}
	_, err = testDB.Exec(
		`UPDATE signals SET payload_json = JSON_OBJECT('title', ?, 'body', ?)
		 WHERE workspace_id = ? AND public_id = ?`,
		"Source title after the provider call started", "Changed while the provider was blocked", workspaceID, inboxItemID[:])
	require.NoError(t, err)
	releaseOnce.Do(func() { close(releaseProvider) })

	var triageResult response
	select {
	case triageResult = <-triageResponse:
	case <-time.After(10 * time.Second):
		t.Fatal("triage request did not finish after the provider was released")
	}
	require.NoError(t, triageResult.err)
	require.Equalf(t, http.StatusOK, triageResult.status, "triage response body=%s", string(triageResult.body))

	var list struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/suggestions",
		tt.AccessToken, nil, &list)
	suggestion := suggestionForAction(t, list.Suggestions, "archive")
	require.Equal(t, inboxItemID.String(), suggestion.InboxItemID)
	require.NotEmpty(t, suggestion.ProposalID)
	require.Equal(t, "captured", suggestion.SourceVersionStatus)

	status, raw := executeSuggestion(t, tt, suggestion, "changed-source-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusConflict, status, "changed source body=%s", string(raw))
	require.Equal(t, "AI.SUGGESTION.EVIDENCE_CHANGED", problemType(t, raw))
}

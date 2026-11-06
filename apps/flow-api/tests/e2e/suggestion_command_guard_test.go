package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/inboxsnapshot"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// proposalSeed describes one persisted proposal owned by an actor in a
// workspace. taskReadable mirrors the triage capture: an unreadable or
// disabled linked task contributes nothing to the source version.
// inboxItemID reuses an existing signal instead of inserting one.
type proposalSeed struct {
	action       string
	taskPublicID string
	taskReadable bool
	inboxItemID  string
}

func seedSuggestionProposal(t *testing.T, workspace, actor *helpers.TestTenant, seed proposalSeed) commandSuggestion {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, workspace.WorkspacePublicID)
	actorID := helpers.ResolveUserInternalID(t, testDB, actor.UserPublicID)

	var taskID any
	var taskPublicID types.PublicID
	var taskTitle string
	if seed.taskPublicID != "" {
		_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, workspace.WorkspacePublicID, seed.taskPublicID)
		require.NoError(t, testDB.QueryRow(
			`SELECT public_id, title FROM tasks WHERE workspace_id = ? AND id = ?`,
			workspaceID, taskInternalID,
		).Scan(&taskPublicID, &taskTitle))
		taskID = taskInternalID
	}

	batchPublicID := uuid.Must(uuid.NewV7())
	inputCount := 0
	if seed.taskReadable {
		inputCount = 1
	}
	batchResult, err := testDB.Exec(
		`INSERT INTO ai_suggestion_batches (public_id, workspace_id, user_id, input_count, provenance_valid)
		 VALUES (?, ?, ?, ?, TRUE)`,
		batchPublicID[:], workspaceID, actorID, inputCount)
	require.NoError(t, err)
	if seed.taskReadable {
		batchID, err := batchResult.LastInsertId()
		require.NoError(t, err)
		_, err = testDB.Exec(
			`INSERT INTO ai_suggestion_batches_inputs (batch_id, task_public_id) VALUES (?, ?)`,
			batchID, taskPublicID[:])
		require.NoError(t, err)
	}

	inboxItemID := uuid.Must(uuid.NewV7())
	if seed.inboxItemID != "" {
		inboxItemID = uuid.MustParse(seed.inboxItemID)
	} else {
		_, err = testDB.Exec(
			`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
			 VALUES (?, ?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3))`,
			inboxItemID[:], workspaceID, taskID)
		require.NoError(t, err)
	}
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
	if seed.taskReadable {
		fields.TaskPublicID = taskPublicID.String()
		fields.TaskTitle = taskTitle
		fields.TaskReadable = true
	}

	proposalID := uuid.Must(uuid.NewV7())
	_, err = testDB.Exec(
		`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, 'ai.suggestion.proposed',
		         JSON_OBJECT('inbox_item_id', ?, 'score', 0.5, 'action', ?, 'reasoning', 'Seeded recommendation.', 'source_version', ?, 'suggestion_batch_id', ?), NOW(3))`,
		proposalID[:], workspaceID, actorID, inboxItemID.String(), seed.action,
		inboxsnapshot.Capture(fields).VersionHash(), batchPublicID.String())
	require.NoError(t, err)
	return commandSuggestion{
		EventID:           proposalID.String(),
		ProposalID:        proposalID.String(),
		InboxItemID:       inboxItemID.String(),
		RecommendedAction: seed.action,
	}
}

func suggestionURL(workspace *helpers.TestTenant, inboxItemID, verb string) string {
	return testServerURL + "/workspaces/" + workspace.WorkspacePublicID + "/ai/suggestions/" + inboxItemID + "/" + verb
}

func executeSuggestionIn(t *testing.T, workspace, actor *helpers.TestTenant, s commandSuggestion, operationKey, action string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPost, suggestionURL(workspace, s.InboxItemID, "execute"),
		actor.AccessToken, commandRequest(s, operationKey, action))
}

func decideSuggestionIn(t *testing.T, workspace, actor *helpers.TestTenant, s commandSuggestion, body map[string]any) (int, []byte) {
	t.Helper()
	body["proposalId"] = s.ProposalID
	body["expectedRevision"] = s.Revision
	return doJSONStatus(t, http.MethodPatch, suggestionURL(workspace, s.InboxItemID, "decision"),
		actor.AccessToken, body)
}

func countSuggestionDecisions(t *testing.T, s commandSuggestion) int64 {
	t.Helper()
	var count int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM ai_suggestion_decisions d
		 INNER JOIN events e ON e.id = d.proposal_event_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)`,
		s.ProposalID,
	).Scan(&count))
	return count
}

func TestAiSuggestionRoleLockLeavesWorkspaceInsertsUnblocked(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	actorID := helpers.ResolveUserInternalID(t, testDB, tt.UserPublicID)
	workspacePublicID, err := types.Parse(tt.WorkspacePublicID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	role, err := generated.New(tx).GetSuggestionWorkspaceRoleForShare(ctx, generated.GetSuggestionWorkspaceRoleForShareParams{
		UserID:            actorID,
		WorkspacePublicID: workspacePublicID,
	})
	require.NoError(t, err)
	require.Equal(t, "owner", string(role))

	conn, err := testDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(ctx, `SET SESSION innodb_lock_wait_timeout = 1`)
	require.NoError(t, err)
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SET SESSION innodb_lock_wait_timeout = DEFAULT`)
	}()

	eventID := uuid.Must(uuid.NewV7())
	_, err = conn.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, actor_user_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, 'signal.snoozed', JSON_OBJECT(), NOW(3))`,
		eventID[:], workspaceID, actorID)
	require.NoError(t, err, "an event insert in the workspace must not wait on a held role lock")
	signalID := uuid.Must(uuid.NewV7())
	_, err = conn.ExecContext(ctx,
		`INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at)
		 VALUES (?, ?, 'manual', 'manual', JSON_OBJECT(), NOW(3))`,
		signalID[:], workspaceID)
	require.NoError(t, err, "a signal insert in the workspace must not wait on a held role lock")

	_, err = conn.ExecContext(ctx,
		`UPDATE workspace_members SET role = 'guest' WHERE workspace_id = ? AND user_id = ?`,
		workspaceID, actorID)
	require.Error(t, err, "a concurrent demotion must wait for the command holding the membership lock")
	require.Contains(t, err.Error(), "Lock wait timeout")
}

func TestAiSuggestionExecuteRejectsActionOtherThanRecommended(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestion := suggestionForAction(t, seedCommandSuggestions(t, tt, ""), "snooze")
	status, raw := executeSuggestion(t, tt, suggestion, "mismatch-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusConflict, status, "mismatched action body=%s", string(raw))
	require.Equal(t, "AI.SUGGESTION.EVIDENCE_CHANGED", problemType(t, raw))
	assertSignalEnabled(t, tt, suggestion.InboxItemID, true)
	require.Zero(t, countSuggestionEvent(t, tt, "signal.archived", suggestion.InboxItemID, suggestion.ProposalID))
	require.Zero(t, countSuggestionEvent(t, tt, "ai.suggestion.applied", suggestion.InboxItemID, suggestion.ProposalID))
}

func TestAiSuggestionDisabledLinkedTaskCountsAsNoTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskWithVisibility(t, tt, "Suggestion task later disabled", "public")
	_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	_, err := testDB.Exec(`UPDATE tasks SET enabled = FALSE WHERE id = ?`, taskInternalID)
	require.NoError(t, err)

	archive := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "archive", taskPublicID: taskID})
	status, raw := executeSuggestion(t, tt, archive, "disabled-archive-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusOK, status, "archive with disabled task body=%s", string(raw))
	assertSignalEnabled(t, tt, archive.InboxItemID, false)

	snooze := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "snooze", taskPublicID: taskID})
	status, raw = executeSuggestion(t, tt, snooze, "disabled-snooze-"+randomHex(8), "snooze")
	require.Equalf(t, http.StatusOK, status, "snooze with disabled task body=%s", string(raw))

	open := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "open", taskPublicID: taskID})
	status, raw = executeSuggestion(t, tt, open, "disabled-open-"+randomHex(8), "open")
	require.Equalf(t, http.StatusNotFound, status, "open with disabled task body=%s", string(raw))
	require.Equal(t, "WS.INBOX.NOT_FOUND", problemType(t, raw))
}

func TestAiSuggestionUnreadableLinkedTaskAllowsOnlySignalActions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	privateTask := createTaskWithVisibility(t, owner, "Suggestion private task", "private")

	archive := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive", taskPublicID: privateTask})
	status, raw := executeSuggestionIn(t, owner, member, archive, "unreadable-archive-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusOK, status, "archive with unreadable task body=%s", string(raw))
	var archived suggestionCommandResult
	require.NoError(t, json.Unmarshal(raw, &archived))
	require.Empty(t, archived.TaskID, "an unreadable task id must not be returned")
	assertSignalEnabled(t, owner, archive.InboxItemID, false)

	snooze := seedSuggestionProposal(t, owner, member, proposalSeed{action: "snooze", taskPublicID: privateTask})
	status, raw = executeSuggestionIn(t, owner, member, snooze, "unreadable-snooze-"+randomHex(8), "snooze")
	require.Equalf(t, http.StatusOK, status, "snooze with unreadable task body=%s", string(raw))

	open := seedSuggestionProposal(t, owner, member, proposalSeed{action: "open", taskPublicID: privateTask})
	status, raw = executeSuggestionIn(t, owner, member, open, "unreadable-open-"+randomHex(8), "open")
	require.Equalf(t, http.StatusNotFound, status, "open with unreadable task body=%s", string(raw))
	require.Equal(t, "WS.INBOX.NOT_FOUND", problemType(t, raw))

	edit := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive", taskPublicID: privateTask})
	status, raw = decideSuggestionIn(t, owner, member, edit, map[string]any{
		"state": "edited", "action": "open", "reasoning": "Open the linked task instead.",
	})
	require.Equalf(t, http.StatusNotFound, status, "edit to open with unreadable task body=%s", string(raw))
	require.Equal(t, "WS.INBOX.NOT_FOUND", problemType(t, raw))
	require.Zero(t, countSuggestionDecisions(t, edit))
}

func TestAiSuggestionDecisionRejectsOpenWithoutLinkedTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suggestion := suggestionForAction(t, seedCommandSuggestions(t, tt, ""), "archive")
	status, raw := decideSuggestionIn(t, tt, tt, suggestion, map[string]any{
		"state": "edited", "action": "open", "reasoning": "Open the linked task instead.",
	})
	require.Equalf(t, http.StatusNotFound, status, "edit to open without task body=%s", string(raw))
	require.Equal(t, "WS.INBOX.NOT_FOUND", problemType(t, raw))
	require.Zero(t, countSuggestionDecisions(t, suggestion))
	require.Zero(t, countSuggestionEvent(t, tt, "ai.suggestion.edited", suggestion.InboxItemID, suggestion.ProposalID))
}

func TestAiSuggestionCommandsFollowRevokedWorkspaceAccess(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	memberID := helpers.ResolveUserInternalID(t, testDB, member.UserPublicID)
	completed := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive"})
	pending := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive"})
	completedKey := "revoked-completed-" + randomHex(8)

	status, raw := executeSuggestionIn(t, owner, member, completed, completedKey, "archive")
	require.Equalf(t, http.StatusOK, status, "member execute body=%s", string(raw))

	setMembership := func(query string) {
		t.Helper()
		_, err := testDB.Exec(query, workspaceID, memberID)
		require.NoError(t, err)
	}
	assertDenied := func(stage string) {
		t.Helper()
		status, raw := executeSuggestionIn(t, owner, member, pending, "revoked-pending-"+randomHex(8), "archive")
		require.Equalf(t, http.StatusForbidden, status, "%s: execute body=%s", stage, string(raw))
		status, raw = executeSuggestionIn(t, owner, member, completed, completedKey, "archive")
		require.Equalf(t, http.StatusForbidden, status, "%s: completed replay body=%s", stage, string(raw))
		status, raw = decideSuggestionIn(t, owner, member, pending, map[string]any{"state": "dismissed"})
		require.Equalf(t, http.StatusForbidden, status, "%s: decision body=%s", stage, string(raw))
		assertSignalEnabled(t, owner, pending.InboxItemID, true)
		require.Zero(t, countSuggestionDecisions(t, pending), stage)
	}

	setMembership(`UPDATE workspace_members SET role = 'guest' WHERE workspace_id = ? AND user_id = ?`)
	assertDenied("demoted to guest")
	setMembership(`UPDATE workspace_members SET role = 'member', enabled = FALSE WHERE workspace_id = ? AND user_id = ?`)
	assertDenied("removed from workspace")
}

func TestAiSuggestionLegacyReactionRoutesAreGone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	proposal := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive"})
	for _, verb := range []string{"apply", "dismiss"} {
		status, raw := doJSONStatus(t, http.MethodPost, suggestionURL(owner, proposal.InboxItemID, verb), owner.AccessToken, nil)
		require.Containsf(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, status,
			"%s must not resolve another actor's proposal; body=%s", verb, string(raw))
	}
	var list struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/suggestions",
		member.AccessToken, nil, &list)
	require.Len(t, list.Suggestions, 1)
	require.Equal(t, proposal.ProposalID, list.Suggestions[0].ProposalID)
}

func TestAiSuggestionCommandsRequireAnInboxItem(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	missing := commandSuggestion{ProposalID: uuid.NewString()}
	for _, id := range []string{uuid.NewString(), "not-an-inbox-item"} {
		missing.InboxItemID = id
		status, raw := executeSuggestion(t, tt, missing, "missing-"+randomHex(8), "archive")
		require.Equalf(t, http.StatusNotFound, status, "execute on %q body=%s", id, string(raw))
		status, raw = decideSuggestionIn(t, tt, tt, missing, map[string]any{"state": "dismissed"})
		require.Equalf(t, http.StatusNotFound, status, "decision on %q body=%s", id, string(raw))
	}
}

func listSuggestionsAs(t *testing.T, workspace, actor *helpers.TestTenant) ([]commandSuggestion, string) {
	t.Helper()
	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+workspace.WorkspacePublicID+"/ai/suggestions", actor.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "list body=%s", string(raw))
	var list struct {
		Suggestions []commandSuggestion `json:"suggestions"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	return list.Suggestions, string(raw)
}

func listedProposalIDs(suggestions []commandSuggestion) []string {
	ids := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		ids = append(ids, s.ProposalID)
	}
	return ids
}

func TestAiSuggestionDismissalRecordsOneDismissedOutcome(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	dismissed := seedSuggestionProposal(t, owner, owner, proposalSeed{action: "archive"})
	other := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive", inboxItemID: dismissed.InboxItemID})

	body := map[string]any{"state": "dismissed"}
	status, raw := decideSuggestionIn(t, owner, owner, dismissed, body)
	require.Equalf(t, http.StatusOK, status, "dismiss body=%s", string(raw))
	require.EqualValues(t, 1, countSuggestionEvent(t, owner, "ai.suggestion.dismissed", dismissed.InboxItemID, dismissed.ProposalID))

	var metrics struct {
		Dismissed int64 `json:"dismissed"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/metrics",
		owner.AccessToken, nil, &metrics)
	require.EqualValues(t, 1, metrics.Dismissed)

	status, raw = decideSuggestionIn(t, owner, owner, dismissed, map[string]any{"state": "dismissed"})
	require.Equalf(t, http.StatusOK, status, "dismiss replay body=%s", string(raw))
	require.EqualValues(t, 1, countSuggestionEvent(t, owner, "ai.suggestion.dismissed", dismissed.InboxItemID, dismissed.ProposalID),
		"replaying the same decision appends nothing")

	ownerList, _ := listSuggestionsAs(t, owner, owner)
	require.NotContains(t, listedProposalIDs(ownerList), dismissed.ProposalID)
	memberList, _ := listSuggestionsAs(t, owner, member)
	require.Contains(t, listedProposalIDs(memberList), other.ProposalID,
		"one actor's dismissal leaves another actor's proposal for the same item pending")
	status, raw = executeSuggestionIn(t, owner, member, other, "after-dismiss-"+randomHex(8), "archive")
	require.Equalf(t, http.StatusOK, status, "other actor execute body=%s", string(raw))
}

func TestAiSuggestionListTreatsUnreadableTaskAsTaskless(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	title := "Unreadable suggestion task " + uuid.NewString()
	privateTask := createTaskWithVisibility(t, owner, title, "private")
	archive := seedSuggestionProposal(t, owner, member, proposalSeed{action: "archive", taskPublicID: privateTask})
	open := seedSuggestionProposal(t, owner, member, proposalSeed{action: "open", taskPublicID: privateTask})

	listed, raw := listSuggestionsAs(t, owner, member)
	require.Contains(t, listedProposalIDs(listed), archive.ProposalID, "archive stays reachable on an unreadable task")
	require.NotContains(t, listedProposalIDs(listed), open.ProposalID, "open on an unreadable task cannot execute")
	require.NotContains(t, raw, title)
	require.NotContains(t, raw, privateTask)

	var reloaded commandSuggestion
	for _, s := range listed {
		if s.ProposalID == archive.ProposalID {
			reloaded = s
		}
	}
	status, body := executeSuggestionIn(t, owner, member, reloaded, "listed-unreadable-"+randomHex(8), reloaded.RecommendedAction)
	require.Equalf(t, http.StatusOK, status, "execute from reloaded list body=%s", string(body))
}

func TestAiSuggestionListTreatsDisabledTaskAsTaskless(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	title := "Disabled suggestion task " + uuid.NewString()
	taskID := createTaskWithVisibility(t, tt, title, "public")
	_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	_, err := testDB.Exec(`UPDATE tasks SET enabled = FALSE WHERE id = ?`, taskInternalID)
	require.NoError(t, err)
	snooze := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "snooze", taskPublicID: taskID})
	open := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "open", taskPublicID: taskID})

	listed, raw := listSuggestionsAs(t, tt, tt)
	require.Contains(t, listedProposalIDs(listed), snooze.ProposalID)
	require.NotContains(t, listedProposalIDs(listed), open.ProposalID, "open on a disabled task cannot execute")
	require.NotContains(t, raw, title)
	require.NotContains(t, raw, taskID)
}

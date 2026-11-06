package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type sourceIntakeResponse struct {
	IntakeItemID string `json:"intakeItemId"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	SourceURL    string `json:"sourceUrl"`
	TaskID       string `json:"taskId"`
	Created      bool   `json:"created"`
}

// TestSourceIntakeWorkflowPreservesSourceAndConvertsIdempotently exercises
// the source-to-work-to-task path through the real HTTP server and MySQL.
// The GitHub fixture is inserted as an already-authenticated signal so this
// test isolates source projection and intake behavior from webhook signing.
func TestSourceIntakeWorkflowPreservesSourceAndConvertsIdempotently(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	signalID := seedSourceSignal(t, tt, "github", "issues.opened", map[string]any{
		"action": "opened",
		"issue": map[string]any{
			"title":    "Restore the launch checklist",
			"body":     "Please restore the missing launch checklist before the release.",
			"html_url": "https://github.com/acme/ops/issues/17",
		},
		"repository": map[string]any{"full_name": "acme/ops"},
	})
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/inbox/" + signalID + "/intake"

	var first sourceIntakeResponse
	status, raw := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, nil)
	require.Equalf(t, http.StatusCreated, status, "source capture body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &first))
	require.NotEmpty(t, first.IntakeItemID)
	require.True(t, first.Created)
	require.Equal(t, "Restore the launch checklist", first.Title)
	require.Equal(t, "Please restore the missing launch checklist before the release.", first.Body)
	require.Equal(t, "https://github.com/acme/ops/issues/17", first.SourceURL)

	// The inbox projection exposes a bounded, safe DTO derived from the
	// authenticated payload. Consumers do not need to inspect raw JSON to
	// find the actionable source title, body, or link.
	var inbox struct {
		Items []struct {
			ID           string `json:"id"`
			TaskID       string `json:"taskId"`
			TaskTitle    string `json:"taskTitle"`
			SourceDetail struct {
				Title     string `json:"title"`
				Body      string `json:"body"`
				SourceURL string `json:"sourceUrl"`
			} `json:"sourceDetail"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &inbox)
	var projected bool
	for _, item := range inbox.Items {
		if item.ID != signalID {
			continue
		}
		projected = true
		require.Equal(t, first.Title, item.SourceDetail.Title)
		require.Equal(t, first.Body, item.SourceDetail.Body)
		require.Equal(t, first.SourceURL, item.SourceDetail.SourceURL)
	}
	require.True(t, projected, "source signal must remain visible in Inbox")

	var replay sourceIntakeResponse
	status, raw = doJSONStatus(t, http.MethodPost, base, tt.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "source capture replay body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &replay))
	require.False(t, replay.Created)
	require.Equal(t, first.IntakeItemID, replay.IntakeItemID,
		"replaying a signal capture must return its existing intake item")

	convertURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake/" + first.IntakeItemID + "/convert"
	convertBody := map[string]any{
		"projectId":       tt.ProjectPublicID,
		"assigneeUserIds": []string{tt.UserPublicID},
		"dueOn":           "2026-12-18",
	}
	var converted struct {
		Ok     bool   `json:"ok"`
		TaskID string `json:"taskId"`
	}
	doJSON(t, http.MethodPost, convertURL, tt.AccessToken, convertBody, &converted)
	require.True(t, converted.Ok)
	require.NotEmpty(t, converted.TaskID)

	var convertedAgain struct {
		Ok     bool   `json:"ok"`
		TaskID string `json:"taskId"`
	}
	doJSON(t, http.MethodPost, convertURL, tt.AccessToken, convertBody, &convertedAgain)
	require.True(t, convertedAgain.Ok)
	require.Equal(t, converted.TaskID, convertedAgain.TaskID,
		"a retry must return the already-created task")

	// Capture replay after conversion must return the task link already
	// established by the conversion transaction.
	var capturedAgain sourceIntakeResponse
	status, raw = doJSONStatus(t, http.MethodPost, base, tt.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "post-conversion capture replay body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &capturedAgain))
	require.False(t, capturedAgain.Created)
	require.Equal(t, converted.TaskID, capturedAgain.TaskID)

	var sources struct {
		Sources []struct {
			IntakeItemID string `json:"intakeItemId"`
			SignalID     string `json:"signalId"`
			Source       string `json:"source"`
			Title        string `json:"title"`
			Body         string `json:"body"`
			SourceURL    string `json:"sourceUrl"`
		} `json:"sources"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+converted.TaskID+"/sources",
		tt.AccessToken, nil, &sources)
	require.Len(t, sources.Sources, 1)
	require.Equal(t, first.IntakeItemID, sources.Sources[0].IntakeItemID)
	require.Equal(t, signalID, sources.Sources[0].SignalID)
	require.Equal(t, "github", sources.Sources[0].Source)
	require.Equal(t, first.Title, sources.Sources[0].Title)
	require.Equal(t, first.Body, sources.Sources[0].Body)
	require.Equal(t, first.SourceURL, sources.Sources[0].SourceURL)

	// Inbox uses signals.task_id as its task association. Conversion must
	// persist that alongside intake_items.task_id so the source leaves the
	// unattached list and can be followed from the task.
	var linkedInbox struct {
		Items []struct {
			ID        string `json:"id"`
			TaskID    string `json:"taskId"`
			TaskTitle string `json:"taskTitle"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &linkedInbox)
	var linkedItem bool
	for _, item := range linkedInbox.Items {
		if item.ID == signalID {
			linkedItem = true
			require.Equal(t, converted.TaskID, item.TaskID)
			require.Equal(t, first.Title, item.TaskTitle)
		}
	}
	require.True(t, linkedItem, "converted source must remain in Inbox")

	var task struct {
		Title       string         `db:"title"`
		Description sql.NullString `db:"description"`
		DueOn       sql.NullString `db:"due_on"`
	}
	require.NoError(t, testDB.QueryRow(
		`SELECT title, description, DATE_FORMAT(due_on, '%Y-%m-%d')
		   FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, converted.TaskID,
	).Scan(&task.Title, &task.Description, &task.DueOn))
	require.Equal(t, first.Title, task.Title)
	require.True(t, task.Description.Valid)
	require.Equal(t, first.Body, task.Description.String)
	require.True(t, task.DueOn.Valid)
	require.Equal(t, "2026-12-18", task.DueOn.String)

	var actors struct {
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+converted.TaskID+"/actors",
		tt.AccessToken, nil, &actors)
	require.Len(t, actors.Actors, 1)
	require.Equal(t, tt.UserPublicID, actors.Actors[0].UserID)
	require.Equal(t, "assignee", actors.Actors[0].Role)

	var linkedTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, first.IntakeItemID,
	).Scan(&linkedTaskID))
	require.True(t, linkedTaskID.Valid, "intake row must retain its task backlink")
	var signalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalTaskID))
	require.True(t, signalTaskID.Valid, "source signal must retain its task backlink")
	var convertedTaskInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, converted.TaskID,
	).Scan(&convertedTaskInternalID))
	require.Equal(t, convertedTaskInternalID, signalTaskID.Int64)
	var subjectType string
	var subjectID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT subject_type, subject_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&subjectType, &subjectID))
	require.Equal(t, "workspace", subjectType)
	require.False(t, subjectID.Valid, "task association must not rewrite the signal subject pair")

	attachedEvents := sourceSignalAttachedEvents(t, signalID)
	require.Len(t, attachedEvents, 1, "conversion and retries must emit one signal.attached event")
	require.True(t, attachedEvents[0].TaskID.Valid)
	require.Equal(t, convertedTaskInternalID, attachedEvents[0].TaskID.Int64)
	var attachedPayload map[string]any
	require.NoError(t, json.Unmarshal(attachedEvents[0].Payload, &attachedPayload))
	require.Equal(t, signalID, attachedPayload["signalId"])
	require.Equal(t, converted.TaskID, attachedPayload["taskId"])
	require.Equal(t, "github", attachedPayload["source"])
	require.Equal(t, "issues.opened", attachedPayload["kind"])

	var taskCreatedEvents, intakeAcceptedEvents int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE task_id = (
		   SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)
		 ) AND type = 'task.created'`, converted.TaskID,
	).Scan(&taskCreatedEvents))
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		  WHERE task_id = (
		    SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)
		  ) AND type = 'intake.item.accepted'`, converted.TaskID,
	).Scan(&intakeAcceptedEvents))
	require.Equal(t, 1, taskCreatedEvents, "a conversion replay must not duplicate task events")
	require.Equal(t, 1, intakeAcceptedEvents, "a conversion replay must not duplicate intake events")
}

// TestSourceIntakeExplicitLinkRepairsMissingSignalAssociation covers an
// authorized retry where the intake already names the requested task but
// signals.task_id is still NULL. The explicit link request may repair that
// missing association exactly once.
func TestSourceIntakeExplicitLinkRepairsMissingSignalAssociation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{
		"title": "Existing intake association",
	})

	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, nil, &captured)
	taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Existing task for source repair")

	var taskInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, taskID,
	).Scan(&taskInternalID))
	_, err := testDB.Exec(
		`UPDATE intake_items
		    SET task_id = ?, triage_status = 'accepted'
		  WHERE public_id = UUID_TO_BIN(?, 0)`, taskInternalID, captured.IntakeItemID,
	)
	require.NoError(t, err)

	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/inbox/" + signalID + "/intake"
	var linked sourceIntakeResponse
	status, raw := doJSONStatus(t, http.MethodPost, url, tt.AccessToken, map[string]any{"existingTaskId": taskID})
	require.Equalf(t, http.StatusOK, status, "repair existing source association body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &linked))
	require.Equal(t, taskID, linked.TaskID)

	var signalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalTaskID))
	require.True(t, signalTaskID.Valid, "explicit same-task link must repair the missing signal association")
	require.Equal(t, taskInternalID, signalTaskID.Int64)
	require.Len(t, sourceSignalAttachedEvents(t, signalID), 1,
		"the repaired signal association must append one signal.attached event")

	status, raw = doJSONStatus(t, http.MethodPost, url, tt.AccessToken, map[string]any{"existingTaskId": taskID})
	require.Equalf(t, http.StatusOK, status, "same-task link replay body=%s", string(raw))
	require.Len(t, sourceSignalAttachedEvents(t, signalID), 1,
		"replaying an already-linked task must not append another signal event")
}

// TestSourceIntakeConversionAdoptsRacingLink holds an uncommitted signal
// association while conversion runs. Once the link commits, conversion must
// observe it under the source lock, create no competing task, and join the
// intake to the task the source already names.
func TestSourceIntakeConversionAdoptsRacingLink(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{
		"title": "Racing source conversion",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, nil, &captured)
	otherTaskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Racing source target task")

	var signalInternalID, otherTaskInternalID int64
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalInternalID))
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, otherTaskID,
	).Scan(&otherTaskInternalID))
	lockTx, err := testDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback() }()
	var lockedSignalID int64
	require.NoError(t, lockTx.QueryRow(
		`SELECT id FROM signals WHERE id = ? FOR UPDATE`, signalInternalID,
	).Scan(&lockedSignalID))
	_, err = lockTx.Exec(`UPDATE signals SET task_id = ? WHERE id = ?`, otherTaskInternalID, lockedSignalID)
	require.NoError(t, err)

	convertURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake/" + captured.IntakeItemID + "/convert"
	convertBody := map[string]any{
		"projectId":       tt.ProjectPublicID,
		"assigneeUserIds": []string{tt.UserPublicID},
	}
	type conversionResult struct {
		status int
		body   []byte
		err    error
	}
	resultCh := make(chan conversionResult, 1)
	go func() {
		status, body, err := sendJSONStatus(http.MethodPost, convertURL, tt.AccessToken, convertBody)
		resultCh <- conversionResult{status: status, body: body, err: err}
	}()

	// Give the request time to reach the locked source row. With the
	// approved lock order it waits there; the existing implementation can
	// incorrectly finish while the conflicting association is uncommitted.
	var earlyResult *conversionResult
	select {
	case result := <-resultCh:
		earlyResult = &result
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, lockTx.Commit())
	var result conversionResult
	if earlyResult != nil {
		result = *earlyResult
	} else {
		select {
		case result = <-resultCh:
		case <-time.After(10 * time.Second):
			t.Fatal("conversion did not finish after the source lock was released")
		}
	}
	require.NoError(t, result.err)
	require.Equalf(t, http.StatusOK, result.status, "racing source link body=%s", string(result.body))
	var adopted struct {
		TaskID string `json:"taskId"`
	}
	require.NoError(t, json.Unmarshal(result.body, &adopted))
	require.Equal(t, otherTaskID, adopted.TaskID)

	var taskCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Racing source conversion'`,
		helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID),
	).Scan(&taskCount))
	require.Zero(t, taskCount, "an adopted conversion must not create a second task")
	var intakeTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&intakeTaskID))
	require.Equal(t, sql.NullInt64{Int64: otherTaskInternalID, Valid: true}, intakeTaskID,
		"the intake must join the task its source already names")
	var retainedSignalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&retainedSignalTaskID))
	require.True(t, retainedSignalTaskID.Valid)
	require.Equal(t, otherTaskInternalID, retainedSignalTaskID.Int64,
		"conversion must leave the already-associated task untouched")
}

// TestSourceIntakeConversionRollsBackOnSignalAttachEventFailure verifies
// the source link and task writes share a transaction with signal.attached.
func TestSourceIntakeConversionRollsBackOnSignalAttachEventFailure(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{
		"title": "Rollback source conversion",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, nil, &captured)
	installSignalAttachedFailure(t, signalID)

	convertURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake/" + captured.IntakeItemID + "/convert"
	status, raw := doJSONStatus(t, http.MethodPost, convertURL, tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID})
	require.Equalf(t, http.StatusInternalServerError, status, "event append failure body=%s", string(raw))
	require.Equal(t, "INTERNAL.UNEXPECTED", decodeErrorCode(t, raw))

	var taskCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Rollback source conversion'`,
		helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID),
	).Scan(&taskCount))
	require.Zero(t, taskCount, "the task must roll back with the failed source event")
	var intakeTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&intakeTaskID))
	require.False(t, intakeTaskID.Valid, "the intake link must roll back with the failed source event")
	var signalTaskID sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT task_id FROM signals WHERE public_id = UUID_TO_BIN(?, 0)`, signalID,
	).Scan(&signalTaskID))
	require.False(t, signalTaskID.Valid, "the signal link must roll back with the failed source event")
	require.Empty(t, sourceSignalAttachedEvents(t, signalID))
	var acceptedEvents int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		  WHERE type = 'intake.item.accepted'
		    AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.intakeItemId')) = ?`, captured.IntakeItemID,
	).Scan(&acceptedEvents))
	require.Zero(t, acceptedEvents, "the accepted event must roll back with the task")
	var createdEvents int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		  WHERE type = 'task.created'
		    AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.title')) = 'Rollback source conversion'`,
	).Scan(&createdEvents))
	require.Zero(t, createdEvents, "the task.created event must roll back with the failed source event")
	var triageStatus string
	require.NoError(t, testDB.QueryRow(
		`SELECT triage_status FROM intake_items WHERE public_id = UUID_TO_BIN(?, 0)`, captured.IntakeItemID,
	).Scan(&triageStatus))
	require.Equal(t, "pending", triageStatus, "the failed conversion must not accept the intake item")
}

type sourceSignalAttachedEvent struct {
	TaskID  sql.NullInt64
	Payload []byte
}

func sourceSignalAttachedEvents(t *testing.T, signalID string) []sourceSignalAttachedEvent {
	t.Helper()
	rows, err := testDB.Query(
		`SELECT task_id, payload_json
		   FROM events
		  WHERE type = 'signal.attached'
		    AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.signalId')) = ?`,
		signalID,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	events := make([]sourceSignalAttachedEvent, 0)
	for rows.Next() {
		var event sourceSignalAttachedEvent
		require.NoError(t, rows.Scan(&event.TaskID, &event.Payload))
		events = append(events, event)
	}
	require.NoError(t, rows.Err())
	return events
}

func installSignalAttachedFailure(t *testing.T, signalID string) {
	t.Helper()
	trigger := "trg_test_signal_attached_" + randomHex(5)
	statement := fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON events FOR EACH ROW
BEGIN
	IF NEW.type = 'signal.attached' AND
	   JSON_UNQUOTE(JSON_EXTRACT(NEW.payload_json, '$.signalId')) = '%s' THEN
		SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced signal attached append failure';
	END IF;
END`, trigger, signalID)
	_, err := testDB.Exec(statement)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, err := testDB.Exec("DROP TRIGGER IF EXISTS " + trigger); err != nil {
			t.Errorf("drop test trigger %s: %v", trigger, err)
		}
	})
}

// TestSourceIntakeEnforcesWorkspaceAndTargetVisibility checks both tenant
// isolation and current task visibility when the capture request asks to
// link existing work. A hidden target's title must not appear in any denial.
func TestSourceIntakeEnforcesWorkspaceAndTargetVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	owner := newTenant(t)
	outsider := newTenant(t)
	signalID := seedSourceSignal(t, owner, "manual", "manual", map[string]any{
		"title": "Confidential escalation",
		"body":  "Private source body",
	})
	url := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/inbox/" + signalID + "/intake"
	status, raw := doJSONStatus(t, http.MethodPost, url, outsider.AccessToken, nil)
	require.Equalf(t, http.StatusForbidden, status, "foreign workspace body=%s", string(raw))

	// The caller must be a workspace member before it can be added to the
	// project or attached to a private task.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost, testServerURL+"/invites/"+invite.Token+"/accept",
		outsider.AccessToken, nil, nil)
	addProjectRoleMember(t, owner, owner.ProjectPublicID, outsider.UserPublicID, "editor")

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Secret target title",
			"visibility": "private",
			"actors": []map[string]any{
				{"userId": outsider.UserPublicID, "role": "assignee"},
			},
		}, &task)

	// The project editor who is also a task actor may link the private task.
	// The source capture, intake backlink, and inbox task link are one action.
	var linked sourceIntakeResponse
	status, raw = doJSONStatus(t, http.MethodPost, url, outsider.AccessToken,
		map[string]any{"existingTaskId": task.ID})
	require.Equalf(t, http.StatusCreated, status, "authorized target link body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &linked))
	require.True(t, linked.Created)
	require.Equal(t, task.ID, linked.TaskID)

	// Remove the private-task grant through the real actor mutation route.
	var actors struct {
		Actors []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, nil, &actors)
	var actorRowID string
	for _, actor := range actors.Actors {
		if actor.UserID == outsider.UserPublicID {
			actorRowID = actor.ID
			break
		}
	}
	require.NotEmpty(t, actorRowID)
	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/actors/"+actorRowID,
		owner.AccessToken, nil, nil)

	// The existing intake link must be rechecked against current task ACL.
	status, raw = doJSONStatus(t, http.MethodPost, url, outsider.AccessToken, nil)
	require.Equalf(t, http.StatusNotFound, status, "revoked target replay body=%s", string(raw))
	require.NotContains(t, string(raw), "Secret target title",
		"refusing to return a revoked private target must not disclose its title")

	// Inbox rows remain visible, but the task ID and title are masked after
	// the actor grant disappears.
	var inbox struct {
		Items []struct {
			ID        string `json:"id"`
			TaskID    string `json:"taskId"`
			TaskTitle string `json:"taskTitle"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+owner.WorkspacePublicID,
		outsider.AccessToken, nil, &inbox)
	var found bool
	for _, item := range inbox.Items {
		if item.ID != signalID {
			continue
		}
		found = true
		require.Empty(t, item.TaskID)
		require.Empty(t, item.TaskTitle)
	}
	require.True(t, found, "source signal must remain visible after target access is revoked")

	status, raw = doJSONStatus(t, http.MethodPost, url, outsider.AccessToken,
		map[string]any{"existingTaskId": task.ID})
	require.Equalf(t, http.StatusNotFound, status, "revoked explicit link body=%s", string(raw))
	require.NotContains(t, string(raw), "Secret target title")
}

// TestSourceIntakeDropsUnsafeSourceURLs checks the only source link exposed
// to clients is an absolute HTTP(S) URL. Raw source payloads remain data and
// are never promoted into rendered link targets.
func TestSourceIntakeDropsUnsafeSourceURLs(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{
		"title": "A safe title",
		"body":  "A normal body",
		"url":   "javascript:alert(document.cookie)",
	})
	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/inbox/" + signalID + "/intake"
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost, url, tt.AccessToken, nil, &captured)
	require.Empty(t, captured.SourceURL)

	var inbox struct {
		Items []struct {
			ID           string `json:"id"`
			SourceDetail struct {
				SourceURL string `json:"sourceUrl"`
			} `json:"sourceDetail"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &inbox)
	for _, item := range inbox.Items {
		if item.ID == signalID {
			require.Empty(t, item.SourceDetail.SourceURL)
			return
		}
	}
	require.Fail(t, "unsafe-link signal must remain in the inbox")
}

func TestSourceIntakeConvertRejectsCrossWorkspaceAssignees(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	foreign := newTenant(t)
	signalID := seedSourceSignal(t, tt, "manual", "manual", map[string]any{
		"title": "Prepare the launch review",
	})
	var captured sourceIntakeResponse
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/inbox/"+signalID+"/intake",
		tt.AccessToken, nil, &captured)

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/intake/"+captured.IntakeItemID+"/convert",
		tt.AccessToken, map[string]any{
			"projectId":       tt.ProjectPublicID,
			"assigneeUserIds": []string{foreign.UserPublicID},
		})
	require.Equalf(t, http.StatusNotFound, status, "cross-workspace assignee body=%s", string(raw))
	require.Equal(t, "WS.MEMBER.NOT_FOUND", decodeErrorCode(t, raw))
}

func seedSourceSignal(t *testing.T, tenant *helpers.TestTenant, source, kind string, payload map[string]any) string {
	t.Helper()
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, tenant.WorkspacePublicID)
	id := types.New()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		 VALUES (?, ?, ?, ?, ?, NOW(3), 'workspace')`,
		id[:], workspaceID, source, kind, raw,
	)
	require.NoError(t, err)
	return id.String()
}

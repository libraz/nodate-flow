package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type calendarEventRequestResult struct {
	status int
	body   []byte
	err    error
}

func postCalendarEventWithHeaders(calURL, token string, body any, header http.Header) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, calURL, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func calendarCreateCount(t *testing.T, workspaceID, calendarID uint32, title string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_events WHERE workspace_id = ? AND calendar_id = ? AND title = ? AND enabled = TRUE`,
		workspaceID, calendarID, title,
	).Scan(&count))
	return count
}

func calendarEventTotalCount(t *testing.T, workspaceID, calendarID uint32, title string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_events WHERE workspace_id = ? AND calendar_id = ? AND title = ?`,
		workspaceID, calendarID, title,
	).Scan(&count))
	return count
}

func calendarCreatedMutationCount(t *testing.T, workspaceID, calendarID uint32, eventID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND calendar_id = ? AND type = 'calendar.event.created' AND subject_public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, calendarID, eventID,
	).Scan(&count))
	return count
}

func calendarCreatedAuditCount(t *testing.T, workspaceID uint32, eventID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'calendar.event.create' AND resource_public_id = UUID_TO_BIN(?, 0)`,
		workspaceID, eventID,
	).Scan(&count))
	return count
}

func TestCalendarEventCreationIdempotency(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Idempotent events")
	calendarURL := fmt.Sprintf("%s/workspaces/%s/calendars/%s/events", testServerURL, owner.WorkspacePublicID, calID)
	workspaceID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	calendarID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	title := "Single event after concurrent retries " + randomHex(6)
	start := time.Date(2035, time.January, 12, 10, 0, 0, 0, time.UTC)
	body := map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}
	key := "calendar-create-" + randomHex(12)
	const requests = 5
	startRequests := make(chan struct{})
	results := make([]calendarEventRequestResult, requests)
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-startRequests
			results[i].status, results[i].body, results[i].err = postCalendarEventWithHeaders(
				calendarURL,
				owner.AccessToken,
				body,
				http.Header{"Idempotency-Key": []string{key}},
			)
		}(i)
	}
	close(startRequests)
	wait.Wait()

	var eventIDs []string
	var firstBody []byte
	for i, result := range results {
		require.NoErrorf(t, result.err, "request %d", i)
		require.Equalf(t, http.StatusOK, result.status, "request %d body=%s", i, result.body)
		var response struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(result.body, &response))
		require.NotEmpty(t, response.ID)
		eventIDs = append(eventIDs, response.ID)
		if i == 0 {
			firstBody = result.body
		} else {
			require.JSONEq(t, string(firstBody), string(result.body), "retries must return the original response snapshot")
		}
	}
	for _, id := range eventIDs[1:] {
		require.Equal(t, eventIDs[0], id, "concurrent retries must resolve to one event")
	}
	require.Equal(t, int64(1), calendarCreateCount(t, workspaceID, calendarID, title))
	require.Equal(t, int64(1), calendarCreatedMutationCount(t, workspaceID, calendarID, eventIDs[0]))
	require.Equal(t, int64(1), calendarCreatedAuditCount(t, workspaceID, eventIDs[0]))

	conflictingBody := map[string]any{
		"kind":     "event",
		"title":    title + " changed",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}
	status, raw, err := postCalendarEventWithHeaders(calendarURL, owner.AccessToken, conflictingBody,
		http.Header{"Idempotency-Key": []string{key}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusConflict, status, "reusing the key with a different body must fail: %s", raw)
	require.Equal(t, "CALENDAR.EVENT.IDEMPOTENCY_KEY_CONFLICT", problemType(t, raw))
	require.Equal(t, int64(1), calendarCreateCount(t, workspaceID, calendarID, title))
	require.Equal(t, int64(0), calendarCreateCount(t, workspaceID, calendarID, title+" changed"))

	otherCalID := createCalendarMut(t, owner, "Idempotency key target check")
	otherCalendarURL := fmt.Sprintf("%s/workspaces/%s/calendars/%s/events", testServerURL, owner.WorkspacePublicID, otherCalID)
	otherCalendarID := helpers.ResolveCalendarInternalID(t, testDB, otherCalID)
	status, raw, err = postCalendarEventWithHeaders(otherCalendarURL, owner.AccessToken, body,
		http.Header{"Idempotency-Key": []string{key}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusConflict, status, "reusing the key for another calendar must fail: %s", raw)
	require.Equal(t, int64(0), calendarCreateCount(t, workspaceID, otherCalendarID, title))

	status, raw, err = postCalendarEventWithHeaders(calendarURL, owner.AccessToken, map[string]any{
		"kind": "event", "title": "Invalid empty key", "timezone": "UTC",
	}, http.Header{"Idempotency-Key": []string{""}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusUnprocessableEntity, status, "a present blank key must fail validation: %s", raw)

	status, raw, err = postCalendarEventWithHeaders(calendarURL, owner.AccessToken, map[string]any{
		"kind": "event", "title": "Invalid repeated key", "timezone": "UTC",
	}, http.Header{"Idempotency-Key": []string{"first", "second"}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusUnprocessableEntity, status, "multiple keys must fail validation: %s", raw)

	aclTitle := "Member access revoked after create " + randomHex(6)
	aclKey := "calendar-create-acl-" + randomHex(8)
	aclBody := map[string]any{"kind": "event", "title": aclTitle, "timezone": "UTC"}
	status, raw, err = postCalendarEventWithHeaders(otherCalendarURL, owner.AccessToken, aclBody,
		http.Header{"Idempotency-Key": []string{aclKey}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, status, "seed the access revocation check: %s", raw)
	ownerID := helpers.ResolveUserInternalID(t, testDB, owner.UserPublicID)
	result, err := testDB.Exec(
		`UPDATE calendar_members SET enabled = FALSE WHERE workspace_id = ? AND calendar_id = ? AND user_id = ?`,
		workspaceID, otherCalendarID, ownerID,
	)
	require.NoError(t, err)
	rowsChanged, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rowsChanged)
	status, raw, err = postCalendarEventWithHeaders(otherCalendarURL, owner.AccessToken, aclBody,
		http.Header{"Idempotency-Key": []string{aclKey}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusForbidden, status, "a replay must require current calendar membership: %s", raw)
	require.Equal(t, int64(1), calendarEventTotalCount(t, workspaceID, otherCalendarID, aclTitle))

	noKeyTitle := "Creation without idempotency key " + randomHex(6)
	noKeyBody := map[string]any{"kind": "event", "title": noKeyTitle, "timezone": "UTC"}
	status, raw, err = postCalendarEventWithHeaders(calendarURL, owner.AccessToken, noKeyBody, nil)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, status, "the original no-key path must still work: %s", raw)
	require.Equal(t, int64(1), calendarCreateCount(t, workspaceID, calendarID, noKeyTitle))

	status, raw = doJSONStatus(t, http.MethodDelete, calendarURL+"/"+eventIDs[0], owner.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "event deletion must succeed: %s", raw)
	status, raw, err = postCalendarEventWithHeaders(calendarURL, owner.AccessToken, body,
		http.Header{"Idempotency-Key": []string{key}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusNotFound, status, "a replay must respect current event visibility: %s", raw)
	require.Equal(t, int64(0), calendarCreateCount(t, workspaceID, calendarID, title))
	require.Equal(t, int64(1), calendarEventTotalCount(t, workspaceID, calendarID, title), "the replay must not recreate a deleted event")
}

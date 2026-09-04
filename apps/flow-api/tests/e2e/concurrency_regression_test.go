package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrentTaskCreatesAllocateDistinctTaskNumbers(t *testing.T) {
	bootstrap(t)

	tt := newTenant(t)
	const workers = 24
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			status, body, err := sendJSONStatus(http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
				"projectId": tt.ProjectPublicID,
				"title":     fmt.Sprintf("Concurrent task %02d", i),
			})
			if err != nil {
				errs <- err
				return
			}
			if status < 200 || status >= 300 {
				errs <- fmt.Errorf("POST /tasks worker %d -> %d body=%s", i, status, string(body))
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var total, distinctNumbers int
	var minNumber, maxNumber int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT task_number), MIN(task_number), MAX(task_number)
		 FROM tasks
		 WHERE project_id = (SELECT id FROM projects WHERE public_id = UUID_TO_BIN(?, 0))
		   AND title LIKE 'Concurrent task %'`,
		tt.ProjectPublicID,
	).Scan(&total, &distinctNumbers, &minNumber, &maxNumber))
	require.Equal(t, workers, total)
	require.Equal(t, workers, distinctNumbers)
	require.Equal(t, 1, minNumber)
	require.Equal(t, workers, maxNumber)
}

func TestConcurrentCalendarInviteCreateConvergesToSingleActiveInvite(t *testing.T) {
	bootstrap(t)

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Concurrent Invite Cal")
	evtID := createEventMut(t, host, calID, "Concurrent Invite Event")
	attendeeID := addAttendeeMut(t, host, calID, evtID, member.UserPublicID)

	const workers = 12
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	url := testServerURL + "/workspaces/" + host.WorkspacePublicID +
		"/calendars/" + calID + "/events/" + evtID + "/attendees/" + attendeeID + "/invite"

	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			status, body, err := sendJSONStatus(http.MethodPost, url, host.AccessToken, map[string]any{})
			if err != nil {
				errs <- err
				return
			}
			if status < 200 || status >= 300 {
				errs <- fmt.Errorf("invite worker %d -> %d body=%s", i, status, string(body))
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var listed struct {
		Invites []struct {
			ID string `json:"id"`
		} `json:"invites"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/invites",
		host.AccessToken, nil, &listed)
	require.Len(t, listed.Invites, 1, "concurrent creates must converge to one active invite")
}

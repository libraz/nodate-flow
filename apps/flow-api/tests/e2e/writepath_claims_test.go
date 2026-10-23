package e2e

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Guarded read-then-write paths: each decision is enforced by the
// statement or the locked transaction that writes, so concurrent callers
// cannot both pass it, and a call that changed nothing records nothing.

// raceCall is one request raceRequests fires.
type raceCall struct {
	method, url, bearer string
	body                any
}

// raceRequests fires the requests at once and returns their statuses in
// the order given. The goroutines only record; every assertion runs on
// the test goroutine after they finish.
func raceRequests(t *testing.T, reqs []raceCall) []int {
	t.Helper()
	out := make([]int, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, r := range reqs {
		wg.Add(1)
		go func(i int, r raceCall) {
			defer wg.Done()
			<-start
			out[i], _, errs[i] = sendJSONStatus(r.method, r.url, r.bearer, r.body)
		}(i, r)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "request %d", i)
	}
	return out
}

// countStatus counts how many of statuses equal want.
func countStatus(statuses []int, want int) int {
	n := 0
	for _, s := range statuses {
		if s == want {
			n++
		}
	}
	return n
}

// taskEventCount counts events of kind recorded against a task.
func taskEventCount(t *testing.T, taskPublicID, kind string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM events e JOIN tasks tk ON tk.id = e.task_id
		WHERE tk.public_id = UUID_TO_BIN(?, 0) AND e.type = ?`, taskPublicID, kind).Scan(&n))
	return n
}

// auditCount counts audit rows of action naming resource.
func auditCount(t *testing.T, action, resourceID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action = ? AND resource_public_id = UUID_TO_BIN(?, 0)`, action, resourceID).Scan(&n))
	return n
}

// TestHandoffEventsReachTheEventbusHooks holds both handoff endpoints to
// the shared recorder: the events they write fan out to every registered
// hook after commit, which is how notifications, webhooks and the stream
// hear about a hand-back.
func TestHandoffEventsReachTheEventbusHooks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	var (
		mu   sync.Mutex
		seen = map[string]int{}
	)
	handle := eventbus.AddNotifyHook(func(_ context.Context, ws uint32, kind string, _ uint64) {
		if ws != wsID {
			return
		}
		mu.Lock()
		seen[kind]++
		mu.Unlock()
	})
	t.Cleanup(func() { eventbus.RemoveNotifyHook(handle) })

	taskID := createTaskForAgent(t, tt, "Handoff fan-out")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/handoff/to-agent", tt.AccessToken,
		map[string]any{"agentId": agent.AgentPublicID}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/handoff/to-user", tt.AccessToken,
		map[string]any{"reason": "manual"}, nil)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["agent.task.handoff_to_agent"] == 1 && seen["agent.task.handoff_to_user"] == 1
	}, 5*time.Second, 50*time.Millisecond, "each handoff event must reach the eventbus hooks exactly once")
	require.Equal(t, 1, auditCount(t, "task.handoff.to_user", taskID))
}

// TestRepeatedArchiveAndDeleteRecordOnce: a repeat or a losing concurrent
// call answers ok and leaves no second event or audit row.
func TestRepeatedArchiveAndDeleteRecordOnce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskIn(t, tt, "Archive twice")
	for i := 0; i < 2; i++ {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/archive", tt.AccessToken, nil, nil)
	}
	require.Equal(t, 1, taskEventCount(t, taskID, "task.archived"))
	require.Equal(t, 1, auditCount(t, "task.archived", taskID))

	doomed := createTaskIn(t, tt, "Delete twice")
	statuses := raceRequests(t, []raceCall{
		{http.MethodDelete, testServerURL + "/tasks/" + doomed, tt.AccessToken, nil},
		{http.MethodDelete, testServerURL + "/tasks/" + doomed, tt.AccessToken, nil},
	})
	for _, s := range statuses {
		require.Contains(t, []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound}, s)
	}
	require.Equal(t, 1, taskEventCount(t, doomed, "item.deleted"), "one delete, one item.deleted event")
	require.Equal(t, 1, auditCount(t, "task.delete", doomed), "one delete, one audit row")
}

// TestConcurrentPatchesOfDifferentFieldsBothPersist: the merge reads the
// row under the lock the write holds, so a PATCH of the title and one of
// the priority landing together both survive.
func TestConcurrentPatchesOfDifferentFieldsBothPersist(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	for round := 0; round < 5; round++ {
		taskID := createTaskIn(t, tt, "Patch race")
		title := "renamed " + randomHex(4)
		raceRequests(t, []raceCall{
			{http.MethodPatch, testServerURL + "/tasks/" + taskID, tt.AccessToken, map[string]any{"title": title}},
			{http.MethodPatch, testServerURL + "/tasks/" + taskID, tt.AccessToken, map[string]any{"priority": 4}},
		})
		var got struct {
			Title    string `json:"title"`
			Priority int32  `json:"priority"`
		}
		doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.AccessToken, nil, &got)
		require.Equal(t, title, got.Title, "round %d: the title PATCH was overwritten", round)
		require.EqualValues(t, 4, got.Priority, "round %d: the priority PATCH was overwritten", round)
	}
}

// TestConcurrentIntakeConvertCreatesOneTask: an item converts at most
// once; the losers create no task and record nothing.
func TestConcurrentIntakeConvertCreatesOneTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	title := "convert race " + randomHex(6)
	itemID := createIntakeItem(t, tt, title)
	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake/" + itemID + "/convert"
	convert := raceCall{http.MethodPost, url, tt.AccessToken, map[string]any{"projectId": tt.ProjectPublicID}}
	statuses := raceRequests(t, []raceCall{convert, convert, convert})
	require.Equal(t, 1, countStatus(statuses, http.StatusOK), "exactly one conversion may win: %v", statuses)
	require.Equal(t, 2, countStatus(statuses, http.StatusConflict), "the losers must be told it was converted: %v", statuses)

	var tasks int
	require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE title = ?`, title).Scan(&tasks))
	require.Equal(t, 1, tasks, "one conversion, one task")
	require.Equal(t, 1, auditCount(t, "intake.convert", itemID), "one conversion, one record")
}

// TestExpiredSnoozeReturnsToPending: once its deadline passes, a snoozed
// item lists under pending again and can be triaged.
func TestExpiredSnoozeReturnsToPending(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/intake"
	expired := createIntakeItem(t, tt, "snooze expired")
	waiting := createIntakeItem(t, tt, "snooze waiting")
	doJSON(t, http.MethodPatch, base+"/"+expired, tt.AccessToken,
		map[string]any{"status": "snoozed", "snoozeUntil": time.Now().Add(-time.Hour).Unix()}, nil)
	doJSON(t, http.MethodPatch, base+"/"+waiting, tt.AccessToken,
		map[string]any{"status": "snoozed", "snoozeUntil": time.Now().Add(time.Hour).Unix()}, nil)

	var pending struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, base+"?status=pending&limit=200", tt.AccessToken, nil, &pending)
	ids := map[string]bool{}
	for _, it := range pending.Items {
		ids[it.ID] = true
	}
	require.True(t, ids[expired], "an expired snooze must list under pending")
	require.False(t, ids[waiting], "an unexpired snooze must stay out of pending")

	status, body := doJSONStatus(t, http.MethodPatch, base+"/"+expired, tt.AccessToken, map[string]any{"status": "accepted"})
	require.Equalf(t, http.StatusOK, status, "an expired snooze must be triageable; body=%s", string(body))
	status, body = doJSONStatus(t, http.MethodPatch, base+"/"+waiting, tt.AccessToken, map[string]any{"status": "accepted"})
	requireDenied(t, status, body, http.StatusConflict, "WS.INTAKE.ALREADY_TRIAGED", "triaging an unexpired snooze")
}

// TestConcurrentReactionAddsNeverFail: identical adds racing on the
// unique key answer created or already-exists, never an internal error.
func TestConcurrentReactionAddsNeverFail(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskIn(t, tt, "Reaction race")
	add := raceCall{http.MethodPost, testServerURL + "/tasks/" + taskID + "/reactions", tt.AccessToken, map[string]any{"emoji": "👍"}}
	statuses := raceRequests(t, []raceCall{add, add, add, add})
	require.Equal(t, 1, countStatus(statuses, http.StatusOK), "one add creates the reaction: %v", statuses)
	require.Equal(t, 3, countStatus(statuses, http.StatusConflict), "the rest are duplicates: %v", statuses)
}

// TestConcurrentLensPatchesKeepBothFields: two patches of different lens
// fields landing together both survive.
func TestConcurrentLensPatchesKeepBothFields(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	for round := 0; round < 5; round++ {
		var lens struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
			"name": "race lens " + randomHex(4), "filter": map[string]any{}, "sort": []any{}, "isDefault": false,
		}, &lens)
		name := "renamed " + randomHex(4)
		desc := "described " + randomHex(4)
		raceRequests(t, []raceCall{
			{http.MethodPatch, base + "/" + lens.ID, tt.AccessToken, map[string]any{"name": name}},
			{http.MethodPatch, base + "/" + lens.ID, tt.AccessToken, map[string]any{"description": desc}},
		})
		var got struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		doJSON(t, http.MethodGet, base+"/"+lens.ID, tt.AccessToken, nil, &got)
		require.Equal(t, name, got.Name, "round %d", round)
		require.Equal(t, desc, got.Description, "round %d", round)
	}
}

// TestOppositeReparentsNeverFormACycle: two pages made each other's
// parent at once; the ancestor walk runs under row locks, so one request
// wins and the other is refused as circular.
func TestOppositeReparentsNeverFormACycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages"
	for round := 0; round < 5; round++ {
		var a, b struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{"title": "cycle a " + randomHex(4)}, &a)
		doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{"title": "cycle b " + randomHex(4)}, &b)
		statuses := raceRequests(t, []raceCall{
			{http.MethodPatch, base + "/" + a.ID, tt.AccessToken, map[string]any{"parentPageId": b.ID}},
			{http.MethodPatch, base + "/" + b.ID, tt.AccessToken, map[string]any{"parentPageId": a.ID}},
		})
		require.Equal(t, 1, countStatus(statuses, http.StatusOK), "round %d: exactly one reparent may land: %v", round, statuses)

		var cycles int
		require.NoError(t, testDB.QueryRow(`
			SELECT COUNT(*) FROM pages x JOIN pages y ON y.id = x.parent_page_id
			WHERE x.parent_page_id IS NOT NULL AND y.parent_page_id = x.id
			  AND x.public_id IN (UUID_TO_BIN(?, 0), UUID_TO_BIN(?, 0))`, a.ID, b.ID).Scan(&cycles))
		require.Zero(t, cycles, "round %d: the two pages must not be each other's parent", round)
	}
}

// TestCalendarCrossDemoteLeavesOneOwner: the two owners of a calendar
// demoting each other at once must leave exactly one owner; the loser is
// refused with the last-owner code.
func TestCalendarCrossDemoteLeavesOneOwner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	coOwner := seedWorkspaceMemberTenant(t, host)
	for round := 0; round < 5; round++ {
		calID := createCalendarMut(t, host, "cross demote "+randomHex(4))
		// Adding a member cannot grant owner; the host promotes them.
		addCalendarMemberWithRole(t, host, calID, coOwner.Email, "editor")
		setCalendarMemberRole(t, host, calID, coOwner.UserPublicID, "owner")
		base := testServerURL + "/workspaces/" + host.WorkspacePublicID + "/calendars/" + calID + "/members/"
		statuses := raceRequests(t, []raceCall{
			{http.MethodPatch, base + coOwner.UserPublicID, host.AccessToken, map[string]any{"role": "editor"}},
			{http.MethodPatch, base + host.UserPublicID, coOwner.AccessToken, map[string]any{"role": "editor"}},
		})
		require.Equal(t, 1, countStatus(statuses, http.StatusOK), "round %d: exactly one demote may land: %v", round, statuses)

		var owners int
		require.NoError(t, testDB.QueryRow(`
			SELECT COUNT(*) FROM calendar_members m JOIN calendars c ON c.id = m.calendar_id
			WHERE c.public_id = UUID_TO_BIN(?, 0) AND m.role = 'owner' AND m.enabled = TRUE`, calID).Scan(&owners))
		require.Equal(t, 1, owners, "round %d: one owner must remain", round)
	}
}

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// TestNotificationFanoutRespectsTaskVisibility is the notification half
// of Layer 4 visibility.
//
// Closing the list endpoints moves the disclosure rather than removing
// it if the fan-out still treats "everyone in the workspace" as the
// recipient set: a notification is generated from the task and reaches
// the bell with the task's identity attached, so a member who may not
// read the task learns about it anyway — and unlike a list, a
// notification arrives unprompted.
//
// The test drives the fan-out hook directly, as the other notification
// e2e tests do, because the production hook is wired in cmd/api/main.go
// and the test server does not mount it.
func TestNotificationFanoutRespectsTaskVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	// An ordinary member of the workspace who is not in the project and
	// is not an actor on the task: the exact person Layer 4 excludes.
	outsider := inviteAndJoinWorkspace(t, owner)

	var privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "Private task the outsider must not hear about",
		"visibility": "private",
	}, &privateTask)
	require.NotEmpty(t, privateTask.ID)

	var publicTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "Public task the outsider may hear about",
		"visibility": "public",
	}, &publicTask)
	require.NotEmpty(t, publicTask.ID)

	ctx := context.Background()
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)
	outsiderInternalID := lookupUserInternalID(ctx, t, testDB, outsider.UserPublicID)

	eventFor := func(taskPublicID string) uint64 {
		t.Helper()
		var id uint64
		err := testDB.QueryRowContext(ctx, `
			SELECT e.id
			FROM events e
			JOIN tasks t ON t.id = e.task_id
			WHERE e.workspace_id = ?
			  AND e.actor_user_id = ?
			  AND t.public_id = UUID_TO_BIN(?, 0)
			  AND e.type = 'task.created'
			ORDER BY e.id DESC
			LIMIT 1
		`, wsInternalID, ownerInternalID, taskPublicID).Scan(&id)
		require.NoError(t, err, "expected a task.created event for %s", taskPublicID)
		return id
	}

	privateEventID := eventFor(privateTask.ID)
	publicEventID := eventFor(publicTask.ID)

	queries := generated.New(testDB)
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	hook := f.Hook()

	// The public task first, so the assertion below distinguishes "the
	// filter works" from "the fan-out is not running at all". Without
	// it, a fan-out that silently dropped every event would pass.
	beforePublic := notificationCountForUser(ctx, t, testDB, outsiderInternalID)
	hook(ctx, wsInternalID, "task.created", publicEventID)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	afterPublic := notificationCountForUser(ctx, t, testDB, outsiderInternalID)
	require.Equal(t, int64(1), afterPublic-beforePublic,
		"a workspace member must be notified about a public task")

	f2 := notification.NewFanout(testDB, queries, email.NoopSender{})
	f2.SetTimeout(5 * time.Second)
	hook2 := f2.Hook()
	beforePrivate := notificationCountForUser(ctx, t, testDB, outsiderInternalID)
	hook2(ctx, wsInternalID, "task.created", privateEventID)
	require.NoError(t, f2.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	afterPrivate := notificationCountForUser(ctx, t, testDB, outsiderInternalID)
	assert.Equal(t, int64(0), afterPrivate-beforePrivate,
		"a member who may not read a private task must not be notified about it (before=%d after=%d)",
		beforePrivate, afterPrivate)

	// Nothing about the private task may name it in the outsider's rows.
	var leaked int
	require.NoError(t, testDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM notifications n
		WHERE n.recipient_user_id = ?
		  AND n.resource_public_id = (SELECT t.public_id FROM tasks t WHERE t.public_id = UUID_TO_BIN(?, 0))
	`, outsiderInternalID, privateTask.ID).Scan(&leaked))
	assert.Zero(t, leaked, "no notification row may reference the private task for this recipient")
}

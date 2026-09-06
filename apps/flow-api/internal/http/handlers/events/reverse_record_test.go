package events

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var sharedDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "events_handler_test"})

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := sharedDB.Start(context.Background())
	require.NoError(t, err)
	return inst.DB
}

// tenant is one workspace with one owner, one project and one agent,
// which is what a reversible event needs: it has to point at a task and
// name an agent as its actor.
type tenant struct {
	workspaceID     uint32
	workspacePublic string
	userID          uint32
	projectID       uint32
	agentID         uint32
}

func seed(ctx context.Context, t *testing.T, db *sql.DB) tenant {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	run := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	// workspaces.slug is globally unique and cut to ten characters, so it
	// is taken off the low-order end: the leading digits of a nanosecond
	// timestamp only change once a second.
	wsPub := types.New()
	wsID := run(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "ws-"+suffix[len(suffix)-10:], "Events "+suffix)
	userID := run(`INSERT INTO users (public_id, email, display_name, locale, timezone)
		VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), "events+"+suffix+"@example.test", "Events Tester")
	run(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		types.New(), wsID, userID)
	projectID := run(`INSERT INTO projects (public_id, workspace_id, slug, name) VALUES (?, ?, ?, ?)`,
		types.New(), wsID, "prj-"+suffix[len(suffix)-10:], "Events "+suffix)

	// events.actor_agent_id is a foreign key, so an LLM-origin event needs
	// a real agent, and an agent needs the model and provider it runs on.
	// None of the three is otherwise read by a reversal.
	providerID := run(`INSERT INTO ai_providers
		(public_id, workspace_id, kind, name, api_key_ciphertext, api_key_prefix, api_key_suffix)
		VALUES (?, ?, 'openai_compat', 'Events Provider', ?, 'sk-xxxxx', 'zzzz')`,
		types.New(), wsID, []byte("ciphertext"))
	modelID := run(`INSERT INTO ai_models
		(public_id, workspace_id, provider_id, name, display_name, context_window)
		VALUES (?, ?, ?, 'events-model', 'Events Model', 8192)`,
		types.New(), wsID, providerID)
	agentID := run(`INSERT INTO ai_agents (public_id, workspace_id, model_id, name, system_prompt)
		VALUES (?, ?, ?, 'Events Agent', 'be useful')`,
		types.New(), wsID, modelID)

	return tenant{
		workspaceID:     wsID,
		workspacePublic: wsPub.String(),
		userID:          userID,
		projectID:       projectID,
		agentID:         agentID,
	}
}

// seedDoneTask writes a task already in `done`, the state the reopen
// rollback walks back out of. derived_state is set on the INSERT because
// the guard trigger only refuses an UPDATE, and the engine path this
// test exercises is the transition, not the seed.
func seedDoneTask(ctx context.Context, t *testing.T, db *sql.DB, tn tenant) (uint32, string) {
	t.Helper()
	pub := types.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, created_by_user_id, title, derived_state)
		 VALUES (?, ?, ?, 1, ?, 'Reversible work', 'done')`,
		pub, tn.workspaceID, tn.projectID, tn.userID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id), pub.String() //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// seedAgentEvent writes the row a reversal targets. actor_agent_id is set
// and the two other actor columns are left NULL, which is the whole
// eligibility rule the handler enforces before it will reverse anything.
func seedAgentEvent(ctx context.Context, t *testing.T, db *sql.DB, tn tenant, taskID uint32, kind eventbus.Kind) string {
	t.Helper()
	pub := types.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, task_id, actor_agent_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, ?, ?, '{}', ?)`,
		pub, tn.workspaceID, taskID, tn.agentID, string(kind), time.Now().UTC())
	require.NoError(t, err)
	return pub.String()
}

// auditCount is the number of audit rows an administrator's query on one
// action name would return, scoped to this test's own workspace so a
// parallel run against the shared database cannot change the answer.
func auditCount(ctx context.Context, t *testing.T, db *sql.DB, tn tenant, action string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		tn.workspaceID, action).Scan(&n))
	return n
}

func deps(db *sql.DB) Deps {
	q := generated.New(db)
	return Deps{DB: db, Queries: q, Mutations: mutationlog.New(db, q)}
}

// workspaceCtx runs the real workspace middleware to obtain the context
// the handler reads. The workspace keys are unexported, so the middleware
// is the only way to populate them, and using it keeps the test on the
// same path a request takes.
func workspaceCtx(t *testing.T, db *sql.DB, tn tenant) context.Context {
	t.Helper()
	route := chi.NewRouteContext()
	route.URLParams.Add("wsId", tn.workspacePublic)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	req = req.WithContext(middleware.WithActor(ctx, tn.userID))

	var resolved context.Context
	rec := httptest.NewRecorder()
	middleware.RequireWorkspaceMember(db)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resolved = r.Context()
	})).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "the seeded owner must resolve as a workspace member: %s", rec.Body.String())
	require.NotNil(t, resolved)
	return resolved
}

const (
	reverseAction    = "event.reverse"
	transitionAction = "task.transition"
)

// TestReverseWithStateRollbackRecordsTheTransition covers the reversal
// that moves a task: undoing an auto-completion walks it back out of
// `done`, so the workspace has two changes to answer for and an audit
// query on either action name has to find its own.
func TestReverseWithStateRollbackRecordsTheTransition(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	taskID, taskPublic := seedDoneTask(ctx, t, db, tn)
	eventPublic := seedAgentEvent(ctx, t, db, tn, taskID, eventbus.TaskAutoCompleted)

	beforeReverse := auditCount(ctx, t, db, tn, reverseAction)
	beforeTransition := auditCount(ctx, t, db, tn, transitionAction)

	out, err := Reverse(deps(db))(workspaceCtx(t, db, tn), &ReverseInput{
		WsID:          tn.workspacePublic,
		EventPublicID: eventPublic,
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.Body.PublicID)

	require.Equal(t, beforeReverse+1, auditCount(ctx, t, db, tn, reverseAction),
		"the reversal must answer an audit query on its action, or nobody can say who undid the event")
	require.Equal(t, beforeTransition+1, auditCount(ctx, t, db, tn, transitionAction),
		"the reversal moved the task's state, so an audit query for who moved it must find this request")

	var state string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT derived_state FROM tasks WHERE id = ?`, taskID).Scan(&state))
	require.Equal(t, "waiting", state, "the rollback is what the transition audit row is claiming happened")

	var resourceType string
	var resourcePublic types.PublicID
	var metadata string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT resource_type, resource_public_id, metadata_json FROM audit_logs
		 WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, transitionAction).Scan(&resourceType, &resourcePublic, &metadata))
	require.Equal(t, "task", resourceType,
		"the transition changed the task, so that is the resource the audit row names")
	require.Equal(t, taskPublic, resourcePublic.String(),
		"the resource is named by public id, and it is the task rather than the event")

	var eventPayload string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, string(eventbus.TaskTransition(taskstate.TransitionReopen))).Scan(&eventPayload))
	require.JSONEq(t, eventPayload, metadata,
		"both rows describe one transition, so they carry one description")
	require.JSONEq(t, fmt.Sprintf(
		`{"taskId":%q,"transition":%q,"fromState":"done","toState":"waiting","reason":%q,"via":%q,"reversed_event_public_id":%q}`,
		taskPublic, taskstate.TransitionReopen, reverseTransitionReason, reverseTransitionVia, eventPublic), metadata,
		"the description carries public ids only")
}

// TestReverseWithoutStateRollbackRecordsNoTransition is the other half of
// the pair. The setup differs only in the kind of the reversed event: it
// maps to no transition, so the task never moves, and an audit row saying
// it did would be a claim about a change that did not happen.
func TestReverseWithoutStateRollbackRecordsNoTransition(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	taskID, _ := seedDoneTask(ctx, t, db, tn)
	eventPublic := seedAgentEvent(ctx, t, db, tn, taskID, eventbus.TaskCommentAdded)

	beforeReverse := auditCount(ctx, t, db, tn, reverseAction)
	beforeTransition := auditCount(ctx, t, db, tn, transitionAction)

	out, err := Reverse(deps(db))(workspaceCtx(t, db, tn), &ReverseInput{
		WsID:          tn.workspacePublic,
		EventPublicID: eventPublic,
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.Body.PublicID)

	require.Equal(t, beforeReverse+1, auditCount(ctx, t, db, tn, reverseAction),
		"the reversal itself happened and must still be answerable by action name")
	require.Equal(t, beforeTransition, auditCount(ctx, t, db, tn, transitionAction),
		"no transition ran, so no audit row may claim one")

	var state string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT derived_state FROM tasks WHERE id = ?`, taskID).Scan(&state))
	require.Equal(t, "done", state, "the task stayed where it was, which is why there is nothing to record")
}

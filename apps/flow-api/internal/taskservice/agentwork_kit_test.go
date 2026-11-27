package taskservice_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

var allAgentTools = []string{
	execution.ToolAddComment, execution.ToolSetPriority, execution.ToolSetDueOn,
	execution.ToolTransitionTask, execution.ToolCreateSubtask,
}

// agentKit is one isolated workspace with an agent assigned to a task,
// every tool granted auto, and the workspace owner as the default
// delegator and trigger user.
type agentKit struct {
	t   *testing.T
	db  *sql.DB
	ctx context.Context
	svc *taskservice.Service
	f   serviceFixture
}

func newAgentKit(t *testing.T) *agentKit {
	t.Helper()
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	k := &agentKit{t: t, db: db, ctx: ctx, svc: serviceFor(db), f: seedServiceFixture(t, db, true)}
	k.grantAll("auto")
	k.delegate(k.f.userID)
	return k
}

func (k *agentKit) mustSQL(query string, args ...any) {
	k.t.Helper()
	_, err := k.db.ExecContext(k.ctx, query, args...)
	require.NoError(k.t, err, "statement %q", query)
}

func (k *agentKit) insertID(query string, args ...any) uint32 {
	k.t.Helper()
	res, err := k.db.ExecContext(k.ctx, query, args...)
	require.NoError(k.t, err, "insert %q", query)
	id, err := res.LastInsertId()
	require.NoError(k.t, err)
	require.True(k.t, id > 0 && id <= math.MaxUint32)
	return uint32(id) //#nosec G115 -- bounds checked above
}

func (k *agentKit) grant(grants map[string]string) {
	k.t.Helper()
	raw, err := json.Marshal(grants)
	require.NoError(k.t, err)
	k.mustSQL(`UPDATE ai_agents SET tools_json = ? WHERE id = ?`, raw, k.f.agentID)
}

func (k *agentKit) grantAll(mode string) {
	k.t.Helper()
	grants := map[string]string{}
	for _, tool := range allAgentTools {
		grants[tool] = mode
	}
	k.grant(grants)
}

// addUser adds a workspace member holding the given project role.
func (k *agentKit) addUser(projectRole string) (uint32, types.PublicID) {
	k.t.Helper()
	pub := types.New()
	id := k.insertID(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		pub, fmt.Sprintf("agentkit+%s@example.test", pub.String()), "Agentkit "+projectRole)
	k.mustSQL(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at) VALUES (?, ?, ?, 'member', NOW())`,
		types.New(), k.f.workspaceID, id)
	k.mustSQL(`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role) VALUES (?, ?, ?, ?, ?)`,
		types.New(), k.f.workspaceID, k.f.projectID, id, projectRole)
	return id, pub
}

func (k *agentKit) delegate(userID uint32) {
	k.t.Helper()
	var v any
	if userID != 0 {
		v = userID
	}
	k.mustSQL(`UPDATE task_actors SET delegated_by_user_id = ? WHERE task_id = ? AND agent_id = ?`, v, k.f.taskID, k.f.agentID)
}

func (k *agentKit) principal() execution.Principal {
	k.t.Helper()
	p, err := execution.NewAgentPrincipal(k.f.workspaceID, k.f.agentID, []string{execution.ScopeWriteWorkspace},
		allAgentTools, []types.PublicID{k.f.taskPublicID}, 0)
	require.NoError(k.t, err)
	return p
}

func (k *agentKit) run(delegator, trigger uint32) taskservice.RunPrincipal {
	return taskservice.RunPrincipal{Agent: k.principal(), DelegatorID: delegator, TriggerUserID: trigger}
}

func newStep() taskservice.StepRef {
	return taskservice.StepRef{RunKey: types.New().String(), Step: 1}
}

// do runs one action as the owner-delegated, owner-triggered run.
func (k *agentKit) do(a taskservice.Action) taskservice.Outcome {
	k.t.Helper()
	return k.doAs(k.run(k.f.userID, k.f.userID), a)
}

func (k *agentKit) doAs(run taskservice.RunPrincipal, a taskservice.Action) taskservice.Outcome {
	k.t.Helper()
	out, err := k.svc.Execute(k.ctx, run, k.f.taskPublicID, a, newStep())
	require.NoError(k.t, err)
	return out
}

type taskSnapshot struct {
	priority int32
	dueOn    sql.NullTime
	state    string
}

func (k *agentKit) task() taskSnapshot {
	k.t.Helper()
	var s taskSnapshot
	require.NoError(k.t, k.db.QueryRowContext(k.ctx, `SELECT priority, due_on, derived_state FROM tasks WHERE id = ?`, k.f.taskID).
		Scan(&s.priority, &s.dueOn, &s.state))
	return s
}

func (k *agentKit) count(query string, args ...any) int {
	k.t.Helper()
	var n int
	require.NoError(k.t, k.db.QueryRowContext(k.ctx, query, args...).Scan(&n))
	return n
}

// events counts the task's events of one type.
func (k *agentKit) events(kind string) int {
	return k.count(`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND type = ?`, k.f.workspaceID, k.f.taskID, kind)
}

// allEvents counts the workspace's events, for "nothing was written".
func (k *agentKit) allEvents() int {
	return k.count(`SELECT COUNT(*) FROM events WHERE workspace_id = ?`, k.f.workspaceID)
}

func (k *agentKit) comments() int {
	return k.count(`SELECT COUNT(*) FROM comments WHERE workspace_id = ? AND task_id = ?`, k.f.workspaceID, k.f.taskID)
}

func (k *agentKit) proposals(status string) int {
	return k.count(`SELECT COUNT(*) FROM agent_action_proposals WHERE workspace_id = ? AND task_id = ? AND status = ?`,
		k.f.workspaceID, k.f.taskID, status)
}

// lastEvent returns the payload and actors of the newest event of a type.
func (k *agentKit) lastEvent(kind string) (map[string]any, sql.NullInt32, sql.NullInt32) {
	k.t.Helper()
	var raw []byte
	var actorUser, actorAgent sql.NullInt32
	require.NoError(k.t, k.db.QueryRowContext(k.ctx, `SELECT payload_json, actor_user_id, actor_agent_id FROM events
WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, k.f.workspaceID, kind).Scan(&raw, &actorUser, &actorAgent))
	var payload map[string]any
	require.NoError(k.t, json.Unmarshal(raw, &payload))
	return payload, actorUser, actorAgent
}

func day(s string) sql.NullTime {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return sql.NullTime{Time: t, Valid: true}
}

func requireOutcome(t *testing.T, want taskservice.StepOutcome, got taskservice.Outcome) {
	t.Helper()
	require.Equal(t, want, got.Result, "outcome %+v", got)
}

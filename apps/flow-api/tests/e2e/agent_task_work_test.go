package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// awFixture is one tenant with an AI model, a scripted provider and a real
// runner over the shared database. Every test builds its own, so parallel
// tests never share a workspace, an agent or a script.
type awFixture struct {
	t       *testing.T
	tt      *helpers.TestTenant
	wsID    uint32
	modelID string
	prov    *helpers.ScriptedProvider
	env     *helpers.AgentEnv
}

func newAwFixture(t *testing.T, script ...string) *awFixture {
	t.Helper()
	tt := newTenant(t)
	prov := helpers.NewScriptedProvider(script...)
	return &awFixture{
		t:       t,
		tt:      tt,
		wsID:    helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID),
		modelID: helpers.CreateAIModel(t, testServerURL, tt),
		prov:    prov,
		env:     helpers.NewAgentEnv(t, testDB, prov),
	}
}

// agent creates a task agent through the API; tools nil keeps the default
// grants (every operation on approval).
func (f *awFixture) agent(tools map[string]string) *helpers.TaskAgent {
	f.t.Helper()
	return helpers.CreateTaskAgent(f.t, testDB, testServerURL, f.tt, f.modelID, helpers.TaskAgentOptions{Tools: tools})
}

func (f *awFixture) agentWith(o helpers.TaskAgentOptions) *helpers.TaskAgent {
	f.t.Helper()
	return helpers.CreateTaskAgent(f.t, testDB, testServerURL, f.tt, f.modelID, o)
}

// task creates a public task in the tenant's project as the owner.
func (f *awFixture) task(title string) string {
	f.t.Helper()
	return createTaskForAgent(f.t, f.tt, title)
}

func (f *awFixture) taskInternalID(taskPub string) uint32 {
	f.t.Helper()
	_, id := helpers.AgentTaskInternalIDs(f.t, testDB, f.tt.WorkspacePublicID, taskPub)
	return id
}

// assign attaches the agent as assignee through the API as the given caller
// and returns the task_actors public id. The assignment's own event is not
// run: callers take mark() after it.
func (f *awFixture) assign(caller *helpers.TestTenant, taskPub string, a *helpers.TaskAgent) string {
	f.t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(f.t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/agents", caller.AccessToken,
		map[string]any{"agentId": a.PublicID, "role": "assignee"}, &out)
	require.NotEmpty(f.t, out.ID)
	return out.ID
}

// mark returns the highest event id so far; runs cover events after it.
func (f *awFixture) mark() uint64 {
	f.t.Helper()
	return helpers.LatestEventID(f.t, testDB, f.wsID)
}

func (f *awFixture) runSince(since uint64) []helpers.TriggeredRun {
	f.t.Helper()
	return f.env.RunTriggered(f.t, f.tt.WorkspacePublicID, since)
}

// comment posts a human comment and returns its public id.
func (f *awFixture) comment(who *helpers.TestTenant, taskPub, body string) string {
	f.t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(f.t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/comments", who.AccessToken,
		map[string]any{"body": body}, &out)
	require.NotEmpty(f.t, out.ID)
	return out.ID
}

type awTask struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Priority     int32  `json:"priority"`
	DueOn        string `json:"dueOn"`
	DerivedState string `json:"derivedState"`
	Visibility   string `json:"visibility"`
}

func (f *awFixture) getTask(taskPub string) awTask {
	f.t.Helper()
	var out awTask
	doJSON(f.t, http.MethodGet, testServerURL+"/tasks/"+taskPub, f.tt.AccessToken, nil, &out)
	return out
}

type awComment struct {
	ID            string `json:"id"`
	AuthorKind    string `json:"authorKind"`
	AuthorAgentID string `json:"authorAgentId"`
	AuthorName    string `json:"authorDisplayName"`
	Body          string `json:"body"`
}

func (f *awFixture) comments(taskPub string) []awComment {
	f.t.Helper()
	var out struct {
		Comments []awComment `json:"comments"`
	}
	doJSON(f.t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/comments", f.tt.AccessToken, nil, &out)
	return out.Comments
}

type awStep struct {
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	ArgsSummary string `json:"argsSummary"`
	Outcome     string `json:"outcome"`
	ProposalID  string `json:"proposalId"`
	ErrorCode   string `json:"errorCode"`
}

// awRun is one run as the agent-runs endpoint reports it: the lifecycle
// event with its payload decoded.
type awRun struct {
	Type    string
	Payload map[string]any
}

// runs lists the task's run events, oldest first.
func (f *awFixture) runs(taskPub string) []awRun {
	f.t.Helper()
	var out struct {
		Runs []struct {
			Type        string `json:"type"`
			PayloadJSON string `json:"payloadJson"`
		} `json:"runs"`
	}
	doJSON(f.t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/agent-runs?limit=100", f.tt.AccessToken, nil, &out)
	runs := make([]awRun, 0, len(out.Runs))
	for i := len(out.Runs) - 1; i >= 0; i-- {
		r := awRun{Type: out.Runs[i].Type, Payload: map[string]any{}}
		require.NoError(f.t, json.Unmarshal([]byte(out.Runs[i].PayloadJSON), &r.Payload))
		runs = append(runs, r)
	}
	return runs
}

// finished returns the payloads of the task's completed and failed runs.
func (f *awFixture) finished(taskPub string) []map[string]any {
	f.t.Helper()
	var out []map[string]any
	for _, r := range f.runs(taskPub) {
		if r.Type == string(eventbus.AiAgentRunCompleted) || r.Type == string(eventbus.AiAgentRunFailed) {
			out = append(out, r.Payload)
		}
	}
	return out
}

func (f *awFixture) steps(taskPub, runKey string) []awStep {
	f.t.Helper()
	var out struct {
		Total int64    `json:"total"`
		Steps []awStep `json:"steps"`
	}
	doJSON(f.t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/agent-runs/"+runKey+"/steps", f.tt.AccessToken, nil, &out)
	require.EqualValues(f.t, len(out.Steps), out.Total)
	return out.Steps
}

// lastRunSteps returns the steps of the most recent finished run.
func (f *awFixture) lastRunSteps(taskPub string) []awStep {
	f.t.Helper()
	done := f.finished(taskPub)
	require.NotEmpty(f.t, done, "no finished run on the task")
	key, _ := done[len(done)-1]["runKey"].(string)
	require.NotEmpty(f.t, key, "finished run carries no runKey: %v", done[len(done)-1])
	return f.steps(taskPub, key)
}

// requireOneCleanRun asserts the trigger path ran exactly one run, that it
// ended without an error, and returns the task's latest finished-run payload.
func (f *awFixture) requireOneCleanRun(runs []helpers.TriggeredRun, taskPub string, a *helpers.TaskAgent) map[string]any {
	f.t.Helper()
	require.Len(f.t, runs, 1)
	require.Equal(f.t, a.ID, runs[0].AgentID)
	require.NoError(f.t, runs[0].Err)
	done := f.finished(taskPub)
	require.NotEmpty(f.t, done)
	return done[len(done)-1]
}

// setPaused flips the agent's kill switch through the API.
func (f *awFixture) setPaused(a *helpers.TaskAgent, paused bool) {
	f.t.Helper()
	doJSON(f.t, http.MethodPost, testServerURL+"/workspaces/"+f.tt.WorkspacePublicID+"/ai/agents/"+a.PublicID+"/pause",
		f.tt.AccessToken, map[string]any{"paused": paused}, nil)
}

// awAuditCount counts audit rows of an action on one resource, split by
// whether a user is the recorded actor.
func awAuditCount(t *testing.T, wsID uint32, action, resourcePub string, userActor bool) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM audit_logs
		WHERE workspace_id = ? AND action = ? AND resource_public_id = UUID_TO_BIN(?, 0)
		  AND (actor_user_id IS NOT NULL) = ?`, wsID, action, resourcePub, userActor).Scan(&n))
	return n
}

// awAuditEventually waits for the (asynchronously written) audit row.
func awAuditEventually(t *testing.T, wsID uint32, action, resourcePub string, userActor bool) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return awAuditCount(t, wsID, action, resourcePub, userActor) == 1
	}, 5*time.Second, 50*time.Millisecond, "audit %s on %s (userActor=%v) not recorded exactly once", action, resourcePub, userActor)
}

func awRunMode(p map[string]any) string {
	s, _ := p["mode"].(string)
	return s
}

func awEnded(p map[string]any) string {
	s, _ := p["endedReason"].(string)
	return s
}

// awRequireUUID asserts the value is a canonical UUID string, never a number.
func awRequireUUID(t *testing.T, v any, what string) {
	t.Helper()
	s, ok := v.(string)
	require.Truef(t, ok, "%s must be a string, got %T (%v)", what, v, v)
	_, err := uuid.Parse(s)
	require.NoErrorf(t, err, "%s must be a UUID, got %q", what, s)
}

// awEventForComment finds the task.comment.added event of one comment.
func awEventForComment(t *testing.T, wsID, taskID uint32, after uint64, commentPub string) helpers.EventRecord {
	t.Helper()
	for _, e := range helpers.EventsOfType(t, testDB, wsID, taskID, string(eventbus.TaskCommentAdded), after) {
		if id, _ := e.Payload["commentId"].(string); id == commentPub {
			return e
		}
	}
	require.FailNow(t, "no comment event", "comment %s", commentPub)
	return helpers.EventRecord{}
}

func awCountRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(query, args...).Scan(&n))
	return n
}

type awProposal struct {
	ID    string `json:"id"`
	Agent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
	Kind string `json:"kind"`
	Args struct {
		Body       string  `json:"body"`
		Priority   *int32  `json:"priority"`
		DueOn      *string `json:"dueOn"`
		Transition string  `json:"transition"`
		Title      string  `json:"title"`
	} `json:"args"`
	Before struct {
		Priority     *int32  `json:"priority"`
		DueOn        *string `json:"dueOn"`
		DerivedState string  `json:"derivedState"`
	} `json:"before"`
	Status         string `json:"status"`
	ErrorCode      string `json:"errorCode"`
	DecisionReason string `json:"decisionReason"`
	DecidedBy      *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"decidedBy"`
	DecidedAt *int64 `json:"decidedAt"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// proposals lists the task's agent proposals as the caller; status is
// "pending" or "all".
func (f *awFixture) proposals(who *helpers.TestTenant, taskPub, status string) []awProposal {
	f.t.Helper()
	var out struct {
		Total     int64        `json:"total"`
		Proposals []awProposal `json:"proposals"`
	}
	doJSON(f.t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/agent-proposals?status="+status, who.AccessToken, nil, &out)
	require.EqualValues(f.t, len(out.Proposals), out.Total)
	return out.Proposals
}

// proposalRows counts agent_action_proposals rows of the task by status.
func (f *awFixture) proposalRows(taskID uint32, status string) int {
	f.t.Helper()
	return awCountRows(f.t, `SELECT COUNT(*) FROM agent_action_proposals WHERE workspace_id = ? AND task_id = ? AND status = ?`,
		f.wsID, taskID, status)
}

// proposalStatus reads one proposal's stored status straight from its row.
func (f *awFixture) proposalStatus(proposalPub string) string {
	f.t.Helper()
	var s string
	require.NoError(f.t, testDB.QueryRow(
		`SELECT status FROM agent_action_proposals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, proposalPub).Scan(&s))
	return s
}

// setProjectRole gives an existing project member a different role: the
// roster call is idempotent for a live member, so the row is removed and added back.
func (f *awFixture) setProjectRole(user *helpers.TestTenant, role string) {
	f.t.Helper()
	doJSON(f.t, http.MethodDelete, testServerURL+"/projects/"+f.tt.ProjectPublicID+"/members/"+user.UserPublicID,
		f.tt.AccessToken, nil, nil)
	doJSON(f.t, http.MethodPost, testServerURL+"/projects/"+f.tt.ProjectPublicID+"/members",
		f.tt.AccessToken, map[string]any{"userId": user.UserPublicID, "role": role}, nil)
}

// differentPriority returns a priority other than the task's current one.
func awOtherPriority(cur int32) int32 {
	return (cur + 2) % 5
}

// TestAgentTaskWork_AutoComment: an assigned agent granted add_comment in
// auto mode writes a comment in its own name when a person comments.
func TestAgentTaskWork_AutoComment(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const reply = "I read the thread and will start on the first step."
	f := newAwFixture(t, helpers.Move(execution.ToolAddComment, map[string]any{"body": reply}), helpers.Finish("replied"))
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Auto comment")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	humanComment := f.comment(f.tt, taskPub, "Please take a look at this")
	runs := f.runSince(since)
	done := f.requireOneCleanRun(runs, taskPub, agent)

	require.Equal(t, "task_work", awRunMode(done))
	require.Equal(t, "done", awEnded(done))
	require.Equal(t, 2, f.prov.Calls(), "one call for the comment, one for finish")

	comments := f.comments(taskPub)
	require.Len(t, comments, 2)
	require.Equal(t, humanComment, comments[0].ID)
	require.Equal(t, "user", comments[0].AuthorKind)
	require.Equal(t, "agent", comments[1].AuthorKind)
	require.Equal(t, agent.PublicID, comments[1].AuthorAgentID)
	require.Equal(t, agent.Name, comments[1].AuthorName)
	require.Equal(t, reply, comments[1].Body)

	var authorKind string
	var authorID, authorAgent sql.NullInt64
	require.NoError(t, testDB.QueryRow(
		`SELECT author_kind, author_id, author_agent_id FROM comments WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, comments[1].ID).Scan(&authorKind, &authorID, &authorAgent))
	require.Equal(t, "agent", authorKind)
	require.False(t, authorID.Valid, "an agent comment has no human author")
	require.EqualValues(t, agent.ID, authorAgent.Int64)

	ev := awEventForComment(t, f.wsID, taskID, since, comments[1].ID)
	require.Equal(t, agent.ID, ev.ActorAgentID)
	require.Zero(t, ev.ActorUserID, "the comment event must not name a user")
	require.Equal(t, "agent", ev.Payload["authorKind"])
	require.Equal(t, agent.PublicID, ev.Payload["authorAgentId"])

	awAuditEventually(t, f.wsID, "comment.create", comments[1].ID, false)
	require.Zero(t, awAuditCount(t, f.wsID, "comment.create", comments[1].ID, true),
		"the audit row of an agent comment must not carry a user actor")

	steps := f.lastRunSteps(taskPub)
	require.Len(t, steps, 2)
	require.Equal(t, "add_comment", steps[0].Tool)
	require.Equal(t, "applied", steps[0].Outcome)
	require.Equal(t, "finish", steps[1].Tool)
}

// TestAgentTaskWork_NotPermitted: a tool the agent holds no grant for
// writes nothing and proposes nothing.
func TestAgentTaskWork_NotPermitted(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Ungranted tool")
	taskID := f.taskInternalID(taskPub)
	before := f.getTask(taskPub)
	f.prov.Push(
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(before.Priority)}),
		helpers.Finish("nothing allowed"),
	)
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	f.comment(f.tt, taskPub, "go ahead")
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)

	steps := f.lastRunSteps(taskPub)
	require.Len(t, steps, 2)
	require.Equal(t, "set_priority", steps[0].Tool)
	require.Equal(t, "not_permitted", steps[0].Outcome)
	require.Empty(t, steps[0].ProposalID)

	require.Zero(t, awCountRows(t, `SELECT COUNT(*) FROM agent_action_proposals WHERE workspace_id = ? AND task_id = ?`, f.wsID, taskID))
	require.Empty(t, f.proposals(f.tt, taskPub, "all"))
	require.Equal(t, before.Priority, f.getTask(taskPub).Priority, "priority must not move")
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since),
		"no task.updated event for a refused tool")
}

// TestAgentTaskWork_Attribution: due-date, transition and subtask writes by
// an agent carry the same events and audit actions as a person's, with the
// agent as actor.
func TestAgentTaskWork_Attribution(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t,
		helpers.Move(execution.ToolSetDueOn, map[string]any{"due_on": "2031-05-17"}),
		helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "start"}),
		helpers.Move(execution.ToolCreateSubtask, map[string]any{"title": "Draft the outline"}),
		helpers.Finish("done"),
	)
	agent := f.agent(helpers.GrantAll("auto"))
	taskPub := f.task("Attribution parent")
	taskID := f.taskInternalID(taskPub)
	require.Equal(t, "open", f.getTask(taskPub).DerivedState)
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	f.comment(f.tt, taskPub, "work on it")
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)

	after := f.getTask(taskPub)
	require.Equal(t, "2031-05-17", after.DueOn)
	require.Equal(t, "waiting", after.DerivedState)

	steps := f.lastRunSteps(taskPub)
	require.Len(t, steps, 4)
	for i, tool := range []string{"set_due_on", "transition_task", "create_subtask"} {
		require.Equal(t, tool, steps[i].Tool)
		require.Equal(t, "applied", steps[i].Outcome, "step %d", i+1)
	}

	updated := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since)
	require.Len(t, updated, 1)
	require.Equal(t, agent.ID, updated[0].ActorAgentID)
	require.Zero(t, updated[0].ActorUserID)
	require.Equal(t, "2031-05-17", updated[0].Payload["dueOn"])
	awAuditEventually(t, f.wsID, "task.update", taskPub, false)

	transitioned := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskTransitionStart), since)
	require.Len(t, transitioned, 1)
	require.Equal(t, agent.ID, transitioned[0].ActorAgentID)
	require.Zero(t, transitioned[0].ActorUserID)
	awAuditEventually(t, f.wsID, "task.transition", taskPub, false)

	created := helpers.EventsOfType(t, testDB, f.wsID, 0, string(eventbus.TaskCreated), since)
	require.Len(t, created, 1, "exactly the subtask is created")
	require.Equal(t, agent.ID, created[0].ActorAgentID)
	require.Zero(t, created[0].ActorUserID)
	require.Equal(t, "Draft the outline", created[0].Payload["title"])
	require.Equal(t, taskPub, created[0].Payload["parentTaskId"])
	childPub, _ := created[0].Payload["taskId"].(string)
	awRequireUUID(t, childPub, "subtask id")
	awAuditEventually(t, f.wsID, "task.create", childPub, false)

	child := f.getTask(childPub)
	require.Equal(t, "Draft the outline", child.Title)
	require.Equal(t, "public", child.Visibility, "the subtask takes the parent's visibility")
}

// TestAgentTaskWork_DelegatorCeiling: auto applies only while the person who
// assigned the agent can make the change themselves.
func TestAgentTaskWork_DelegatorCeiling(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(map[string]string{execution.ToolSetPriority: "auto"})
	delegator := seedProjectRoleMember(t, f.tt, "editor")

	stillEditor := f.task("Delegator is an editor")
	demoted := f.task("Delegator loses editor")
	f.assign(delegator, stillEditor, agent)
	f.assign(delegator, demoted, agent)
	wantA := awOtherPriority(f.getTask(stillEditor).Priority)
	wantB := awOtherPriority(f.getTask(demoted).Priority)
	f.prov.Push(
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": wantA}), helpers.Finish("a"),
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": wantB}), helpers.Finish("b"),
	)

	since := f.mark()
	f.comment(f.tt, stillEditor, "please set the priority")
	f.requireOneCleanRun(f.runSince(since), stillEditor, agent)
	require.Equal(t, wantA, f.getTask(stillEditor).Priority, "an editor delegator lets auto apply")
	require.Empty(t, f.proposals(f.tt, stillEditor, "all"))

	f.setProjectRole(delegator, "commenter")
	since = f.mark()
	beforeB := f.getTask(demoted).Priority
	f.comment(f.tt, demoted, "please set the priority")
	f.requireOneCleanRun(f.runSince(since), demoted, agent)

	require.Equal(t, beforeB, f.getTask(demoted).Priority, "a demoted delegator must not let the write through")
	pending := f.proposals(f.tt, demoted, "pending")
	require.Len(t, pending, 1)
	require.Equal(t, "set_priority", pending[0].Kind)
	require.Equal(t, "pending", pending[0].Status)
	require.NotNil(t, pending[0].Args.Priority)
	require.Equal(t, wantB, *pending[0].Args.Priority)
	require.Equal(t, agent.PublicID, pending[0].Agent.ID)
	steps := f.lastRunSteps(demoted)
	require.Equal(t, "proposed", steps[0].Outcome)
	require.Equal(t, pending[0].ID, steps[0].ProposalID)
}

// TestAgentTaskWork_NoDelegatorDowngrades: an assignment that records no
// delegator never applies on its own.
func TestAgentTaskWork_NoDelegatorDowngrades(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(map[string]string{execution.ToolSetPriority: "auto"})
	withDelegator := f.task("Assigned through the API")
	noDelegator := f.task("Assigned with no delegator")
	f.assign(f.tt, withDelegator, agent)
	helpers.AssignAgentToTask(t, testDB, f.tt.WorkspacePublicID, noDelegator, agent.ID)
	wantA := awOtherPriority(f.getTask(withDelegator).Priority)
	wantB := awOtherPriority(f.getTask(noDelegator).Priority)
	f.prov.Push(
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": wantA}), helpers.Finish("a"),
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": wantB}), helpers.Finish("b"),
	)

	since := f.mark()
	f.comment(f.tt, withDelegator, "set it")
	f.requireOneCleanRun(f.runSince(since), withDelegator, agent)
	require.Equal(t, wantA, f.getTask(withDelegator).Priority, "control: with a delegator the same step applies")

	before := f.getTask(noDelegator).Priority
	since = f.mark()
	f.comment(f.tt, noDelegator, "set it")
	f.requireOneCleanRun(f.runSince(since), noDelegator, agent)
	require.Equal(t, before, f.getTask(noDelegator).Priority)
	pending := f.proposals(f.tt, noDelegator, "pending")
	require.Len(t, pending, 1)
	require.Equal(t, "set_priority", pending[0].Kind)
	require.Equal(t, wantB, *pending[0].Args.Priority)
}

// TestAgentTaskWork_TriggerUserCeiling: a commenter's comment can start a run
// but cannot make the agent do what only an editor may.
func TestAgentTaskWork_TriggerUserCeiling(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t,
		helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "start"}), helpers.Finish("a"),
		helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "start"}), helpers.Finish("b"),
	)
	agent := f.agent(map[string]string{execution.ToolTransitionTask: "auto"})
	commenter := seedProjectRoleMember(t, f.tt, "commenter")
	byEditor := f.task("Started by an owner comment")
	byCommenter := f.task("Started by a commenter comment")
	f.assign(f.tt, byEditor, agent)
	f.assign(f.tt, byCommenter, agent)

	since := f.mark()
	f.comment(f.tt, byEditor, "start this")
	f.requireOneCleanRun(f.runSince(since), byEditor, agent)
	require.Equal(t, "waiting", f.getTask(byEditor).DerivedState, "control: an editor trigger applies")

	since = f.mark()
	f.comment(commenter, byCommenter, "start this")
	f.requireOneCleanRun(f.runSince(since), byCommenter, agent)
	require.Equal(t, "open", f.getTask(byCommenter).DerivedState, "a commenter trigger must not move the task")
	pending := f.proposals(f.tt, byCommenter, "pending")
	require.Len(t, pending, 1)
	require.Equal(t, "transition_task", pending[0].Kind)
	require.Equal(t, "start", pending[0].Args.Transition)
	require.Equal(t, "open", pending[0].Before.DerivedState)
}

// TestAgentTaskWork_NoMentions: text an agent writes never becomes a mention
// or a mention notification, while the same text from a person does.
func TestAgentTaskWork_NoMentions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Mention host")
	taskID := f.taskInternalID(taskPub)
	mention := "@[Owner](user:" + f.tt.UserPublicID + ")"
	f.prov.Push(helpers.Move(execution.ToolAddComment, map[string]any{"body": "Ping " + mention}), helpers.Finish("pinged"))
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	f.comment(f.tt, taskPub, "go")
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)

	comments := f.comments(taskPub)
	require.Len(t, comments, 2)
	require.Contains(t, comments[1].Body, mention, "the agent's text is stored as written")
	require.Zero(t, awCountRows(t, `SELECT COUNT(*) FROM mentions WHERE workspace_id = ? AND task_id = ?`, f.wsID, taskID))
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.MentionCreated), since))

	f.comment(f.tt, taskPub, "ping "+mention)
	require.Equal(t, 1, awCountRows(t, `SELECT COUNT(*) FROM mentions WHERE workspace_id = ? AND task_id = ?`, f.wsID, taskID),
		"control: a person's identical mention is recorded")
}

// TestAgentTaskWork_InvocationAudience: a task-work call is logged as an
// agent_task_step on the task, readable by the task's audience, and carries
// no title of a private child.
func TestAgentTaskWork_InvocationAudience(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, helpers.Finish("looked"))
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Invocation parent")
	taskID := f.taskInternalID(taskPub)
	publicTitle := "Visible child " + randomHex(4)
	privateTitle := "Secret child " + randomHex(4)
	for title, visibility := range map[string]string{publicTitle: "public", privateTitle: "private"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks", f.tt.AccessToken, map[string]any{
			"projectId": f.tt.ProjectPublicID, "parentTaskId": taskPub, "title": title, "visibility": visibility,
		}, nil)
	}
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	f.comment(f.tt, taskPub, "what is left?")
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)

	var audience, purpose, prompt string
	var rowTask, rowAgent sql.NullInt64
	require.NoError(t, testDB.QueryRow(`
		SELECT audience, purpose, prompt_redacted, task_id, agent_id FROM ai_invocations
		WHERE workspace_id = ? AND purpose = 'agent_task_step'`, f.wsID).Scan(&audience, &purpose, &prompt, &rowTask, &rowAgent))
	require.Equal(t, "task", audience)
	require.EqualValues(t, taskID, rowTask.Int64)
	require.EqualValues(t, agent.ID, rowAgent.Int64)
	require.Contains(t, prompt, publicTitle)
	require.NotContains(t, prompt, privateTitle)
	for _, req := range f.prov.Requests() {
		require.NotContains(t, req.Prompt, privateTitle)
		require.NotContains(t, req.System, privateTitle)
	}
	var inputs int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM ai_invocations_inputs i JOIN ai_invocations v ON v.id = i.invocation_id
		WHERE v.workspace_id = ? AND v.purpose = 'agent_task_step' AND i.task_public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, taskPub).Scan(&inputs))
	require.Equal(t, 1, inputs, "the invocation names the task it quoted")
}

// TestAgentTaskWork_NoLoop: two agents that both subscribe to the events
// each other's writes produce still run exactly once per human comment.
func TestAgentTaskWork_NoLoop(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	subscribe := []string{string(eventbus.TaskCommentAdded), string(eventbus.TaskUpdated)}
	newAgent := func() *helpers.TaskAgent {
		return f.agentWith(helpers.TaskAgentOptions{
			ScheduleKind: "on_event", EventTriggerTypes: subscribe,
			Tools: map[string]string{execution.ToolAddComment: "auto", execution.ToolSetPriority: "auto"},
		})
	}
	a, b := newAgent(), newAgent()
	taskPub := f.task("Two agents")
	taskID := f.taskInternalID(taskPub)
	cur := f.getTask(taskPub).Priority
	f.assign(f.tt, taskPub, a)
	f.assign(f.tt, taskPub, b)
	f.prov.Push(
		helpers.Move(execution.ToolAddComment, map[string]any{"body": "first agent here"}),
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(cur)}),
		helpers.Finish("a done"),
		helpers.Move(execution.ToolAddComment, map[string]any{"body": "second agent here"}),
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(awOtherPriority(cur))}),
		helpers.Finish("b done"),
	)

	since := f.mark()
	f.comment(f.tt, taskPub, "both of you, please")
	runs := f.runSince(since)

	require.Len(t, runs, 2, "one run per agent for the one human comment")
	require.ElementsMatch(t, []uint32{a.ID, b.ID}, []uint32{runs[0].AgentID, runs[1].AgentID})
	for _, r := range runs {
		require.NoError(t, r.Err)
	}
	require.Equal(t, 2, len(helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AiAgentRunStarted), since)))
	require.Len(t, f.comments(taskPub), 3, "the human comment and one reply per agent")
	require.Len(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since), 2,
		"each agent's priority change happened, and woke nobody")
	for _, p := range f.finished(taskPub) {
		require.Equal(t, "task_work", awRunMode(p))
		require.Equal(t, "done", awEnded(p))
	}
}

// TestAgentTaskWork_TasklessTickUnchanged: a run with no task behind it is
// still one model call and a note, never a task-work loop.
func TestAgentTaskWork_TasklessTickUnchanged(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, "Nothing to report this hour.")
	agent := f.agentWith(helpers.TaskAgentOptions{ScheduleKind: "interval"})

	require.NoError(t, f.env.RunJob(t, agentruntime.Job{AgentID: agent.ID, WsID: f.wsID}))

	require.Equal(t, 1, f.prov.Calls())
	require.Equal(t, 1, helpers.CountInvocations(t, testDB, f.wsID, "agent_tick"))
	require.Zero(t, helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step"))
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, 0, string(eventbus.AgentTaskStep), 0))
	completed := helpers.EventsOfType(t, testDB, f.wsID, 0, string(eventbus.AiAgentRunCompleted), 0)
	require.Len(t, completed, 1)
	require.Equal(t, "tick", awRunMode(completed[0].Payload))
	require.Equal(t, agent.ID, completed[0].ActorAgentID)
	require.Nil(t, completed[0].Payload["endedReason"], "a tick has no task-work ending")
}

// TestAgentTaskWork_Coalesce: three comments recorded before the first run
// starts are all read by that run; the other two runs end coalesced without
// calling the model.
func TestAgentTaskWork_Coalesce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, helpers.Finish("read all three"))
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Chatty task")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	for _, body := range []string{"first thought", "second thought", "third thought"} {
		f.comment(f.tt, taskPub, body)
	}
	runs := f.runSince(since)
	require.Len(t, runs, 3)
	for _, r := range runs {
		require.NoError(t, r.Err)
	}

	done := f.finished(taskPub)
	require.Len(t, done, 3)
	require.Equal(t, "done", awEnded(done[0]))
	require.Equal(t, "coalesced", awEnded(done[1]))
	require.Equal(t, "coalesced", awEnded(done[2]))
	require.Equal(t, 1, f.prov.Calls(), "only the first run calls the model")

	prompt := f.prov.Requests()[0].Prompt
	for _, body := range []string{"first thought", "second thought", "third thought"} {
		require.Contains(t, prompt, body, "the first run reads every comment")
	}

	comments := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskCommentAdded), since)
	require.Len(t, comments, 3)
	for i, p := range done {
		awRequireUUID(t, p["contextEventId"], "contextEventId")
		var onTask, id uint64
		require.NoError(t, testDB.QueryRow(
			`SELECT task_id, id FROM events WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
			f.wsID, p["contextEventId"]).Scan(&onTask, &id), "run %d names an event that exists", i+1)
		require.EqualValues(t, taskID, onTask)
		require.GreaterOrEqualf(t, id, comments[2].ID, "run %d's context reaches the newest comment", i+1)
	}
}

// TestAgentTaskWork_CoalesceSkipsFailed: a run that ended on unusable model
// output covers nothing, so the next event gets its own run.
func TestAgentTaskWork_CoalesceSkipsFailed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, "not json at all", "still not json", helpers.Finish("second try"))
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Flaky model")
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	f.comment(f.tt, taskPub, "one")
	f.comment(f.tt, taskPub, "two")
	runs := f.runSince(since)
	require.Len(t, runs, 2)

	done := f.finished(taskPub)
	require.Len(t, done, 2)
	require.Equal(t, "invalid_output", awEnded(done[0]))
	require.Equal(t, "done", awEnded(done[1]), "the second event is not folded into the failed run")
	require.Equal(t, 3, f.prov.Calls(), "two calls for the repair retry, one for the second run")
}

// TestAgentTaskWork_RateLimited: more than 20 task-work runs of one agent on
// one task inside a day end rate_limited without a model call.
func TestAgentTaskWork_RateLimited(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	f.prov.Route(func(providers.Request) (string, bool) { return "unusable answer", true })
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Hammered task")
	f.assign(f.tt, taskPub, agent)

	const maxRuns = 20
	since := f.mark()
	for i := 0; i <= maxRuns; i++ {
		f.comment(f.tt, taskPub, "ping "+randomHex(3))
	}
	runs := f.runSince(since)
	require.Len(t, runs, maxRuns+1)

	done := f.finished(taskPub)
	require.Len(t, done, maxRuns+1)
	for i, p := range done[:maxRuns] {
		require.Equalf(t, "invalid_output", awEnded(p), "run %d", i+1)
	}
	require.Equal(t, "rate_limited", awEnded(done[maxRuns]))
	require.Equal(t, 2*maxRuns, f.prov.Calls(), "the limited run never reaches the model")
}

func awDecode(t *testing.T, raw []byte, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(raw, out), "decode body=%s", string(raw))
}

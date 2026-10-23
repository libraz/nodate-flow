package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// The tests in this file hold MCP tools to the semantics of the REST
// operations they answer for: the same access floor, the same claim on a
// guarded write, the same refusal code and the same shape of result a
// caller can feed back into the next tool.

// rsProjectID returns a project's internal id.
func rsProjectID(t *testing.T, db *sql.DB, pub uuid.UUID) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, db.QueryRow(`SELECT id FROM projects WHERE public_id = ?`, pub[:]).Scan(&id))
	return id
}

// rsTaskID returns a task's internal id.
func rsTaskID(t *testing.T, db *sql.DB, pub uuid.UUID) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, db.QueryRow(`SELECT id FROM tasks WHERE public_id = ?`, pub[:]).Scan(&id))
	return id
}

// rsInsertTask seeds a task in the fixture project and returns its public id.
func rsInsertTask(t *testing.T, db *sql.DB, fx *mcpProjectRoleFixture, number int, title, visibility string) uuid.UUID {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	_, err := db.Exec(
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		pub[:], fx.wsID, rsProjectID(t, db, fx.projectPub), number, title, visibility, fx.editorID)
	require.NoError(t, err)
	return pub
}

// rsInsertAdmin adds a workspace admin with no project_members row.
func rsInsertAdmin(t *testing.T, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	res, err := db.Exec(`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		pub[:], "rs-admin-"+uuid.New().String()[:8]+"@example.test", "RS Admin")
	require.NoError(t, err)
	id64, err := res.LastInsertId()
	require.NoError(t, err)
	id := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32
	mpub := uuid.Must(uuid.NewV7())
	_, err = db.Exec(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'admin')`,
		mpub[:], wsID, id)
	require.NoError(t, err)
	return id
}

// rsCount runs a COUNT(*) query.
func rsCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(q, args...).Scan(&n))
	return n
}

// rsResult reads a tool result as the JSON a client receives.
func rsResult(t *testing.T, out any) map[string]any {
	t.Helper()
	b, err := json.Marshal(out)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// capturingProvider answers every completion with a fixed body and keeps
// the prompts it was sent.
type capturingProvider struct {
	text    string
	mu      sync.Mutex
	prompts []string
}

func (p *capturingProvider) Name() string         { return "capturing" }
func (p *capturingProvider) Kind() providers.Kind { return providers.Kind("openai_compat") }
func (p *capturingProvider) Model() string        { return "capturing-model" }
func (p *capturingProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, req.Prompt)
	p.mu.Unlock()
	return &providers.Response{Model: "capturing-model", Text: p.text}, nil
}

func capturingOrchestrator(p *capturingProvider) *ai.Orchestrator {
	return &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return p, nil
		}),
	}
}

func TestMCPToolsMatchRESTAccessAndListing(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	scopes := []string{"read:workspace", "write:workspace"}

	t.Run("list_projects/a member without a project role does not see the project", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		adminID := rsInsertAdmin(t, db, fx.wsID)

		listed := func(userID uint32) string {
			out, err := mcp.RunListProjects(ctx, deps, mcp.NewTestSession(userID, fx.wsID, scopes), mcpVisJSON(t, map[string]any{}))
			require.NoError(t, err)
			return mcpVisJSONString(t, out)
		}
		require.Contains(t, listed(fx.editorID), fx.projectPub.String(), "a project member sees the project")
		require.Contains(t, listed(adminID), fx.projectPub.String(), "an admin sees every project")
		require.NotContains(t, listed(fx.outsiderID), fx.projectPub.String(),
			"a member with no project_members row must not see the project, as on REST")
	})

	t.Run("list_pages/offset reaches the rows past the first window", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		sess := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		for _, title := range []string{"Alpha", "Bravo", "Charlie"} {
			_, err := mcp.RunCreatePage(ctx, deps, sess, mcpVisJSON(t, map[string]any{"title": title}))
			require.NoError(t, err)
		}
		out, err := mcp.RunListPages(ctx, deps, sess, mcpVisJSON(t, map[string]any{"limit": 2, "offset": 2}))
		require.NoError(t, err)
		res := rsResult(t, out)
		listed, ok := res["pages"].([]any)
		require.True(t, ok)
		require.Len(t, listed, 1, "the third page is reachable through offset")
		require.EqualValues(t, 3, res["total"], "the total covers every page, not the window")
	})

	t.Run("project floor/writes into a project refuse a member without the editor role", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		prj := fx.projectPub.String()
		for _, userID := range []uint32{fx.outsiderID, fx.viewerID} {
			sess := mcp.NewTestSession(userID, fx.wsID, scopes)
			_, err := mcp.RunCreatePage(ctx, deps, sess, mcpVisJSON(t, map[string]any{"title": "Smuggled", "projectId": prj}))
			requireSpec(t, err, apierrors.WsProjectAccessDenied)
			// Refused before the model is asked: no AI is wired, so a
			// later check would answer AI.PROVIDER.NOT_CONFIGURED instead.
			_, err = mcp.RunGeneratePage(ctx, deps, sess, mcpVisJSON(t, map[string]any{"contextDescription": "x", "projectId": prj}))
			requireSpec(t, err, apierrors.WsProjectAccessDenied)
			_, err = mcp.RunCreateTimebox(ctx, deps, sess, mcpVisJSON(t, map[string]any{
				"name": "Sprint " + uuid.New().String()[:6], "startsOn": "2030-01-01", "endsOn": "2030-01-14", "projectId": prj,
			}))
			requireSpec(t, err, apierrors.WsProjectAccessDenied)
			_, err = mcp.RunCreateImportJob(ctx, deps, sess, mcpVisJSON(t, map[string]any{"source": "csv", "projectId": prj}))
			requireSpec(t, err, apierrors.WsProjectAccessDenied)
		}
		prjID := rsProjectID(t, db, fx.projectPub)
		require.Zero(t, rsCount(t, db, `SELECT COUNT(*) FROM pages WHERE project_id = ?`, prjID))
		require.Zero(t, rsCount(t, db, `SELECT COUNT(*) FROM timeboxes WHERE project_id = ?`, prjID))
		require.Zero(t, rsCount(t, db, `SELECT COUNT(*) FROM import_jobs WHERE project_id = ?`, prjID))

		// The control: the editor may write there.
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		out, err := mcp.RunCreatePage(ctx, deps, editor, mcpVisJSON(t, map[string]any{"title": "Runbook", "projectId": prj}))
		require.NoError(t, err)
		pageID, _ := rsResult(t, out)["id"].(string)

		// And a page already in the project is held to the same floor.
		_, err = mcp.RunUpdatePage(ctx, deps, mcp.NewTestSession(fx.viewerID, fx.wsID, scopes),
			mcpVisJSON(t, map[string]any{"pageId": pageID, "title": "Rewritten by a viewer"}))
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("create_page/nesting past the page depth limit is refused", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		parent := ""
		var refused error
		created := 0
		// The limit is a handful of levels; a chain one past every level it
		// could allow has to have been refused somewhere along it.
		for level := range pages.MaxPageDepth + 2 {
			args := map[string]any{"title": "Level " + string(rune('A'+level))}
			if parent != "" {
				args["parentPageId"] = parent
			}
			out, err := mcp.RunCreatePage(ctx, deps, editor, mcpVisJSON(t, args))
			if err != nil {
				refused = err
				break
			}
			created++
			parent, _ = rsResult(t, out)["id"].(string)
		}
		require.GreaterOrEqual(t, created, pages.MaxPageDepth, "pages within the limit are created")
		requireSpec(t, refused, apierrors.PagePageMaxDepth)
		require.Equal(t, created, rsCount(t, db, `SELECT COUNT(*) FROM pages WHERE workspace_id = ?`, fx.wsID),
			"the refused page is not written")
	})

	t.Run("add_task_to_timebox/a completed timebox is closed", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		out, err := mcp.RunCreateTimebox(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"name": "Done sprint", "startsOn": "2030-01-01", "endsOn": "2030-01-14",
		}))
		require.NoError(t, err)
		tbID, _ := rsResult(t, out)["id"].(string)
		tbUUID := uuid.MustParse(tbID)
		_, err = db.Exec(`UPDATE timeboxes SET status = 'completed' WHERE public_id = ?`, tbUUID[:])
		require.NoError(t, err)

		_, err = mcp.RunAddTaskToTimebox(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"timeboxId": tbID, "taskId": fx.taskPub.String(),
		}))
		requireSpec(t, err, apierrors.TimeboxTimeboxAlreadyCompleted)
		require.Zero(t, rsCount(t, db, `SELECT COUNT(*) FROM timebox_tasks WHERE task_id = ?`, rsTaskID(t, db, fx.taskPub)),
			"a refused add writes no link row")
	})

	t.Run("add_task_label/scope, duplicate and unknown label answer as REST does", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)

		// A label scoped to a different project.
		otherPrj := uuid.Must(uuid.NewV7())
		res, err := db.Exec(`INSERT INTO projects (public_id, workspace_id, slug, name, identifier) VALUES (?, ?, ?, ?, ?)`,
			otherPrj[:], fx.wsID, "rs-other-"+uuid.New().String()[:8], "Other", "RO"+uuid.New().String()[:3])
		require.NoError(t, err)
		otherID, err := res.LastInsertId()
		require.NoError(t, err)
		scoped := uuid.Must(uuid.NewV7())
		_, err = db.Exec(`INSERT INTO labels (public_id, workspace_id, project_id, name) VALUES (?, ?, ?, 'other-only')`,
			scoped[:], fx.wsID, otherID)
		require.NoError(t, err)
		_, err = mcp.RunAddTaskLabel(ctx, deps, editor, mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "labelId": scoped.String()}))
		requireSpec(t, err, apierrors.WsLabelScopeMismatch)

		wide := uuid.Must(uuid.NewV7())
		_, err = db.Exec(`INSERT INTO labels (public_id, workspace_id, name) VALUES (?, ?, 'everywhere')`, wide[:], fx.wsID)
		require.NoError(t, err)
		arg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "labelId": wide.String()})
		_, err = mcp.RunAddTaskLabel(ctx, deps, editor, arg)
		require.NoError(t, err)
		_, err = mcp.RunAddTaskLabel(ctx, deps, editor, arg)
		requireSpec(t, err, apierrors.WsLabelAlreadyAttached)

		_, err = mcp.RunAddTaskLabel(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(), "labelId": uuid.Must(uuid.NewV7()).String(),
		}))
		requireSpec(t, err, apierrors.WsLabelNotFound)
	})
}

func TestMCPTaskWritesMatchREST(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	scopes := []string{"read:workspace", "write:workspace"}

	t.Run("update_task/an empty dueOn or startOn clears the date", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		_, err := mcp.RunUpdateTask(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(), "dueOn": "2030-05-01", "startOn": "2030-04-01",
		}))
		require.NoError(t, err)
		_, err = mcp.RunUpdateTask(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(), "dueOn": "", "startOn": "",
		}))
		require.NoError(t, err)
		var due, start sql.NullTime
		require.NoError(t, db.QueryRow(`SELECT due_on, started_on FROM tasks WHERE public_id = ?`, fx.taskPub[:]).Scan(&due, &start))
		require.False(t, due.Valid, "an empty dueOn clears the due date, as REST and the CLI do")
		require.False(t, start.Valid, "an empty startOn clears the start date")
	})

	t.Run("update_task/concurrent edits of different fields both persist", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		for round := range 10 {
			title := "Round " + string(rune('A'+round))
			priority := round%4 + 1
			titleArg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "title": title})
			priorityArg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "priority": priority})
			var wg sync.WaitGroup
			var titleErr, priorityErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, titleErr = mcp.RunUpdateTask(ctx, deps, editor, titleArg)
			}()
			go func() {
				defer wg.Done()
				_, priorityErr = mcp.RunUpdateTask(ctx, deps, editor, priorityArg)
			}()
			wg.Wait()
			require.NoError(t, titleErr)
			require.NoError(t, priorityErr)
			var gotTitle string
			var gotPriority int
			require.NoError(t, db.QueryRow(`SELECT title, priority FROM tasks WHERE public_id = ?`, fx.taskPub[:]).Scan(&gotTitle, &gotPriority))
			require.Equalf(t, title, gotTitle, "round %d: the title edit was overwritten by a stale read", round)
			require.Equalf(t, priority, gotPriority, "round %d: the priority edit was overwritten by a stale read", round)
		}
	})

	t.Run("search_tasks/LIKE metacharacters match literally", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		literal := rsInsertTask(t, db, fx, 101, "rate_limit 100% done", "public")
		lookalike := rsInsertTask(t, db, fx, 102, "rateXlimit 1000 done", "public")
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		for _, q := range []string{"rate_limit", "100%"} {
			out, err := mcp.RunSearchTasks(ctx, deps, editor, mcpVisJSON(t, map[string]any{"query": q}))
			require.NoError(t, err)
			got := mcpVisJSONString(t, out)
			require.Containsf(t, got, literal.String(), "%q must find the task holding it literally", q)
			require.NotContainsf(t, got, lookalike.String(), "%q must not act as a wildcard", q)
		}
	})

	t.Run("archive_task/a repeat records once and names the task", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		arg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String()})
		for range 2 {
			_, err := mcp.RunArchiveTask(ctx, deps, editor, arg)
			require.NoError(t, err)
		}
		taskID := rsTaskID(t, db, fx.taskPub)
		require.Equal(t, 1, rsCount(t, db,
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'task.archived' AND task_id = ?`, fx.wsID, taskID),
			"one archive happened, so one event names it, on the task's own timeline")
		require.Equal(t, 1, rsCount(t, db,
			`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'task.archived'`, fx.wsID))
	})

	t.Run("transition_task/completing a blocker satisfies the dependent's constraint", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		dependent := rsInsertTask(t, db, fx, 103, "Waits on the blocker", "public")
		blockerID, dependentID := rsTaskID(t, db, fx.taskPub), rsTaskID(t, db, dependent)
		edge := uuid.Must(uuid.NewV7())
		_, err := db.Exec(`INSERT INTO task_dependencies (public_id, workspace_id, from_task_id, to_task_id, kind) VALUES (?, ?, ?, ?, 'blocks')`,
			edge[:], fx.wsID, dependentID, blockerID)
		require.NoError(t, err)
		cons := uuid.Must(uuid.NewV7())
		_, err = db.Exec(`INSERT INTO task_constraints (public_id, workspace_id, task_id, kind, expression) VALUES (?, ?, ?, 'dependency', ?)`,
			cons[:], fx.wsID, dependentID, `{"op":"dependency.all_done","taskIds":["`+fx.taskPub.String()+`"]}`)
		require.NoError(t, err)

		_, err = mcp.RunTransitionTask(ctx, deps, mcp.NewTestSession(fx.editorID, fx.wsID, scopes),
			mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "transition": "complete"}))
		require.NoError(t, err)

		var satisfied sql.NullTime
		require.NoError(t, db.QueryRow(`SELECT satisfied_at FROM task_constraints WHERE public_id = ?`, cons[:]).Scan(&satisfied))
		require.True(t, satisfied.Valid, "the dependent's dependency constraint must be re-evaluated after the blocker completes")
	})

	t.Run("apply_steps/children of a private parent stay private", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		parent := rsInsertTask(t, db, fx, 104, "Private plan", "private")
		out, err := mcp.RunApplySteps(ctx, deps, mcp.NewTestSession(fx.editorID, fx.wsID, scopes), mcpVisJSON(t, map[string]any{
			"parentTaskId": parent.String(),
			"steps":        []map[string]any{{"title": "Step one"}, {"title": "Step two"}},
		}))
		require.NoError(t, err)
		created, ok := rsResult(t, out)["created"].([]any)
		require.True(t, ok)
		require.Len(t, created, 2)
		for _, id := range created {
			u := uuid.MustParse(id.(string))
			var vis string
			require.NoError(t, db.QueryRow(`SELECT visibility FROM tasks WHERE public_id = ?`, u[:]).Scan(&vis))
			require.Equal(t, "private", vis, "a step derived from a private task must not be wider than it")
		}
	})
}

func TestMCPAIToolsMatchREST(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()
	scopes := []string{"read:workspace", "write:workspace"}

	t.Run("propose_priority/the result is the scale update_task takes", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		deps := mcp.Deps{DB: db, Queries: generated.New(db), AI: cannedOrchestrator(`{"priority":"high"}`)}
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		out, err := mcp.RunProposePriority(ctx, deps, editor, mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String()}))
		require.NoError(t, err)
		res := rsResult(t, out)
		require.EqualValues(t, 3, res["priority"], "a proposal of high is priority 3")
		require.NotContains(t, res, "rationale", "no always-empty field is returned")

		_, err = mcp.RunUpdateTask(ctx, deps, editor, mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "priority": res["priority"]}))
		require.NoError(t, err, "the proposal is accepted by update_task unchanged")
	})

	t.Run("propose_steps/the result applies unchanged and the model sees the existing children", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		parentID := rsTaskID(t, db, fx.taskPub)
		child := uuid.Must(uuid.NewV7())
		_, err := db.Exec(`INSERT INTO tasks (public_id, workspace_id, project_id, parent_task_id, task_number, title, visibility, created_by_user_id)
			VALUES (?, ?, ?, ?, 105, 'Existing child step', 'public', ?)`,
			child[:], fx.wsID, rsProjectID(t, db, fx.projectPub), parentID, fx.editorID)
		require.NoError(t, err)

		prov := &capturingProvider{text: `[{"title":"Write the plan","description":"Outline it","priority":"high"},{"title":"Review","description":"","priority":"low"}]`}
		deps := mcp.Deps{DB: db, Queries: generated.New(db), AI: capturingOrchestrator(prov)}
		editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		out, err := mcp.RunProposeSteps(ctx, deps, editor, mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String()}))
		require.NoError(t, err)
		res := rsResult(t, out)

		require.NotEmpty(t, prov.prompts)
		require.Contains(t, prov.prompts[0], "Existing child step", "the model must be told which children exist, as REST tells it")

		_, err = mcp.RunApplySteps(ctx, deps, editor, mcpVisJSON(t, map[string]any{
			"parentTaskId": res["parentTaskId"],
			"steps":        res["steps"],
		}))
		require.NoError(t, err, "apply_steps must accept what propose_steps returned")
		var priority int
		require.NoError(t, db.QueryRow(`SELECT priority FROM tasks WHERE parent_task_id = ? AND title = 'Write the plan'`, parentID).Scan(&priority))
		require.Equal(t, 3, priority)
	})

	t.Run("smart_create_task/each subtask id is the task made from that proposal", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		deps := mcp.Deps{
			DB:       db,
			Queries:  generated.New(db),
			AI:       cannedOrchestrator(`{"suggestedAssignees":[],"subtasks":[{"title":"First"},{"title":"   "},{"title":"Third"}]}`),
			Embedder: embed.New(embed.NewMockProvider(), generated.New(db)),
		}
		out, err := mcp.RunSmartCreateTask(ctx, deps, mcp.NewTestSession(fx.editorID, fx.wsID, scopes), mcpVisJSON(t, map[string]any{
			"projectId": fx.projectPub.String(), "title": "Parent",
		}))
		require.NoError(t, err)
		subtasks, ok := rsResult(t, out)["subtasks"].([]any)
		require.True(t, ok)
		require.Len(t, subtasks, 3)

		titleOf := func(id string) string {
			u := uuid.MustParse(id)
			var title string
			require.NoError(t, db.QueryRow(`SELECT title FROM tasks WHERE public_id = ?`, u[:]).Scan(&title))
			return title
		}
		first := subtasks[0].(map[string]any)
		blank := subtasks[1].(map[string]any)
		third := subtasks[2].(map[string]any)
		require.Equal(t, "First", titleOf(first["id"].(string)))
		require.NotContains(t, blank, "id", "a dropped subtask names no task")
		require.Equal(t, "Third", titleOf(third["id"].(string)), "a later subtask must not take a shifted id")
	})

	t.Run("propose_lens/budget and unparseable answers carry REST's codes", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		sess := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		for _, tc := range []struct {
			provider lensProviderFunc
			want     *apierrors.Spec
		}{
			{func(context.Context, string) ([]byte, error) { return nil, nlquery.ErrBudgetExceeded }, apierrors.AiCostGuardExceeded},
			{func(context.Context, string) ([]byte, error) { return []byte(`not a lens`), nil }, apierrors.AiNlQueryUnparseable},
		} {
			deps := mcp.Deps{DB: db, Queries: generated.New(db), NlQuery: nlquery.New(tc.provider)}
			_, err := mcp.RunProposeLens(ctx, deps, sess, mcpVisJSON(t, map[string]any{"prompt": "open tasks"}))
			requireSpec(t, err, tc.want)
		}
	})
}

// lensProviderFunc adapts a function to the lens compiler's provider.
type lensProviderFunc func(ctx context.Context, prompt string) ([]byte, error)

func (f lensProviderFunc) CompileLens(ctx context.Context, prompt string) ([]byte, error) {
	return f(ctx, prompt)
}

func TestMCPClaimedWritesMatchREST(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	scopes := []string{"read:workspace", "write:workspace"}

	insertIntake := func(t *testing.T, wsID uint32, status string, snoozeUntil any) uuid.UUID {
		t.Helper()
		pub := uuid.Must(uuid.NewV7())
		_, err := db.Exec(`INSERT INTO intake_items (public_id, workspace_id, title, triage_status, snooze_until) VALUES (?, ?, 'Inbox entry', ?, ?)`,
			pub[:], wsID, status, snoozeUntil)
		require.NoError(t, err)
		return pub
	}

	t.Run("intake/an expired snooze is pending again", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		expired := insertIntake(t, fx.wsID, "snoozed", time.Now().Add(-time.Hour))
		waiting := insertIntake(t, fx.wsID, "snoozed", time.Now().Add(24*time.Hour))
		sess := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)

		out, err := mcp.RunListIntakeItems(ctx, deps, sess, mcpVisJSON(t, map[string]any{"status": "pending"}))
		require.NoError(t, err)
		listed := mcpVisJSONString(t, out)
		require.Contains(t, listed, expired.String(), "a snooze past its deadline lists under pending")
		require.NotContains(t, listed, waiting.String(), "an unexpired snooze stays out of pending")
		require.NotContains(t, listed, "aiScore", "no field nothing populates is reported")

		_, err = mcp.RunTriageIntakeItem(ctx, deps, sess, mcpVisJSON(t, map[string]any{"intakeItemId": expired.String(), "status": "accepted"}))
		require.NoError(t, err, "an expired snooze can be triaged")
		_, err = mcp.RunTriageIntakeItem(ctx, deps, sess, mcpVisJSON(t, map[string]any{"intakeItemId": waiting.String(), "status": "accepted"}))
		requireSpec(t, err, apierrors.WsIntakeAlreadyTriaged)
	})

	t.Run("intake/concurrent conversions create one task", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		item := insertIntake(t, fx.wsID, "pending", nil)
		sess := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		arg := mcpVisJSON(t, map[string]any{"intakeItemId": item.String(), "projectId": fx.projectPub.String()})

		var wg sync.WaitGroup
		var ok, refused atomic.Int32
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := mcp.RunConvertIntakeToTask(ctx, deps, sess, arg)
				var ae *apierrors.APIError
				switch {
				case err == nil:
					ok.Add(1)
				case stderrors.As(err, &ae) && ae.Spec == apierrors.WsIntakeAlreadyConverted:
					refused.Add(1)
				}
			}()
		}
		wg.Wait()
		require.EqualValues(t, 1, ok.Load(), "exactly one conversion wins")
		require.EqualValues(t, 3, refused.Load(), "every loser is told the item was already converted")
		require.Equal(t, 1, rsCount(t, db,
			`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Inbox entry'`, fx.wsID))
	})

	t.Run("favorites and reactions/concurrent identical adds all succeed", func(t *testing.T) {
		t.Parallel()
		fx := seedMCPProjectRoleFixture(t, db)
		sess := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)
		favArg := mcpVisJSON(t, map[string]any{"targetType": "project", "targetId": fx.projectPub.String()})
		reactArg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String(), "emoji": "👍"})

		var wg sync.WaitGroup
		var failures atomic.Int32
		for range 6 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				if _, err := mcp.RunAddFavorite(ctx, deps, sess, favArg); err != nil {
					failures.Add(1)
				}
			}()
			go func() {
				defer wg.Done()
				if _, err := mcp.RunAddReaction(ctx, deps, sess, reactArg); err != nil {
					failures.Add(1)
				}
			}()
		}
		wg.Wait()
		require.Zero(t, failures.Load(), "a lost duplicate race is the duplicate answer, never a failure")
	})
}

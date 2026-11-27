package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/router"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

// SeededAgent is the bundle of identifiers returned by SeedAgent and
// AssignAgent. It exposes both internal ids (needed by integration tests
// that simulate the orchestrator runner directly) and public_ids (needed
// for any caller that goes through the REST API).
type SeededAgent struct {
	// AgentID is the ai_agents.id internal FK. Tests that simulate the
	// orchestrator (which receives uint32 ids over its Job channel) need
	// the internal id; the REST surface only ever takes the public id.
	AgentID uint32
	// AgentPublicID is the UUID v7 (canonical string) used by every
	// REST endpoint that targets the agent.
	AgentPublicID string
	// ProviderID / ModelID / ModelPublicID are exposed so tests that
	// want to create additional agents inside the same workspace can
	// reuse the dependency chain rather than seeding it twice.
	ProviderID    uint32
	ModelID       uint32
	ModelPublicID string
	// Name is the human-readable label stored on ai_agents.name. Tests
	// assert against this when verifying the timeline / inbox surfaces
	// render the agent's display name.
	Name string
}

// SeedAgent provisions the minimum ai_providers + ai_models + ai_agents
// rows required to attach an agent to a task in tests.
//
// The handler-level POST /workspaces/{wsId}/ai/providers endpoint
// requires a real encryption Cipher and an encrypted api key, neither
// of which is wired through the test harness. Instead this helper
// inserts the rows directly so the seed stays fully self-contained.
//
// Each call uses random suffixes for the provider / model / agent names
// so parallel-running tests inside the same workspace never collide.
// Returns a SeededAgent that carries both the internal and public ids.
func SeedAgent(t *testing.T, db *sql.DB, workspacePublicID string, opts SeedAgentOptions) *SeededAgent {
	t.Helper()
	require.NotNil(t, db, "SeedAgent requires a *sql.DB; call RegisterCleanupDB or pass testDB explicitly")
	require.NotEmpty(t, workspacePublicID, "SeedAgent requires a workspace public id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wsID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID)
	require.NoError(t, err, "lookup workspace id for %s", workspacePublicID)

	suffix := randomHex(6)

	providerPub := types.New()
	modelPub := types.New()
	agentPub := types.New()

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// The three inserts below run in dependency order, each referencing
	// the id the previous one returned, so foreign key enforcement stays
	// on. Switching it off would make a broken seed look like a working
	// one, and because the session variable outlives a rollback it would
	// hand the connection back to the pool with enforcement disabled for
	// whatever test picks it up next.

	// ai_providers — api_key_ciphertext stays a meaningless byte blob
	// because the orchestrator path under test (handoff) never calls
	// the LLM. Production handlers reject empty ciphertext via the
	// Cipher abstraction; we bypass that here intentionally.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO ai_providers (
			public_id, workspace_id, kind, name,
			api_key_ciphertext, api_key_prefix, api_key_suffix,
			default_model, enabled
		) VALUES (?, ?, 'openai_compat', ?, ?, 'sk-test_', 'XXXX', NULL, TRUE)
	`, providerPub, wsID, fmt.Sprintf("test-provider-%s", suffix), []byte("test"))
	require.NoError(t, err, "insert ai_providers")
	providerInternalID := lastInsertID(t, res)

	// ai_models — minimal viable row with non-zero context window so the
	// agent runtime won't trip its sanity checks if a later code path
	// reads them.
	res, err = tx.ExecContext(ctx, `
		INSERT INTO ai_models (
			public_id, workspace_id, provider_id, name, display_name,
			context_window, max_output_tokens,
			input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
			supports_tools, supports_vision, enabled
		) VALUES (?, ?, ?, ?, ?, 128000, 4096, 0, 0, FALSE, FALSE, TRUE)
	`, modelPub, wsID, providerInternalID,
		fmt.Sprintf("test-model-%s", suffix),
		fmt.Sprintf("Test Model %s", suffix),
	)
	require.NoError(t, err, "insert ai_models")
	modelInternalID := lastInsertID(t, res)

	agentName := opts.Name
	if agentName == "" {
		agentName = fmt.Sprintf("Test Agent %s", suffix)
	}
	costCapNull := sql.NullInt32{}
	if opts.MonthlyCostCapCents > 0 {
		costCapNull = sql.NullInt32{Int32: int32(opts.MonthlyCostCapCents), Valid: true} //#nosec G115 -- test fixture
	}
	agentKind := opts.Kind
	if agentKind == "" {
		agentKind = "task_agent"
	}
	// event_trigger_types defaults to JSON_ARRAY() (wildcard); callers
	// that want to scope a signal_judge agent to specific kinds pass
	// a JSON literal in opts.EventTriggerTypes.
	eventTriggerExpr := "JSON_ARRAY()"
	var eventTriggerArgs []interface{}
	if opts.EventTriggerTypes != "" {
		eventTriggerExpr = "CAST(? AS JSON)"
		eventTriggerArgs = append(eventTriggerArgs, opts.EventTriggerTypes)
	}
	// A task agent stores its grants as an object, empty when nothing is
	// granted; only a signal judge has none to store.
	toolsExpr := "JSON_OBJECT()"
	if agentKind == "signal_judge" {
		toolsExpr = "NULL"
	}
	insertSQL := `
		INSERT INTO ai_agents (
			public_id, workspace_id, model_id, kind, name, description,
			system_prompt, temperature, monthly_cost_cap_cents,
			event_trigger_types, tools_json,
			schedule_kind, paused, enabled
		) VALUES (?, ?, ?, ?, ?, ?, ?, 100, ?, ` + eventTriggerExpr + `, ` + toolsExpr + `, 'manual', FALSE, TRUE)
	`
	args := append([]interface{}{
		agentPub, wsID, modelInternalID, agentKind, agentName,
		"Test agent for handoff E2E",
		"You are an integration-test agent. Do not call any tools.",
		costCapNull,
	}, eventTriggerArgs...)
	res, err = tx.ExecContext(ctx, insertSQL, args...)
	require.NoError(t, err, "insert ai_agents")
	agentInternalID := lastInsertID(t, res)

	require.NoError(t, tx.Commit())

	return &SeededAgent{
		AgentID:       uint32(agentInternalID), //#nosec G115 -- test fixture
		AgentPublicID: agentPub.UUID().String(),
		ProviderID:    uint32(providerInternalID), //#nosec G115 -- test fixture
		ModelID:       uint32(modelInternalID),    //#nosec G115 -- test fixture
		ModelPublicID: modelPub.UUID().String(),
		Name:          agentName,
	}
}

// SeedAgentOptions tunes the SeedAgent insert. Zero-value fields fall
// back to sensible defaults so callers only specify what they need.
type SeedAgentOptions struct {
	// Name overrides the auto-generated "Test Agent <hex>" label.
	Name string
	// MonthlyCostCapCents stamps ai_agents.monthly_cost_cap_cents. Use
	// a low value (e.g. 1) for the cost_cap handoff path.
	MonthlyCostCapCents int
	// Kind overrides the ai_agents.kind column. Empty defaults to
	// 'task_agent' to preserve every existing call site. Set to
	// 'signal_judge' to seed a judge agent.
	Kind string
	// EventTriggerTypes overrides ai_agents.event_trigger_types. Empty
	// leaves the column as the default JSON_ARRAY() (wildcard). Pass a
	// JSON array literal (e.g. `["manual","discord.presence"]`) to
	// scope a signal_judge agent to specific signal kinds.
	EventTriggerTypes string
}

// AssignAgentToTask attaches the seeded agent as the task's enabled
// assignee via task_actors. The helper uses direct SQL because the
// task-actors REST endpoints currently target human users; the
// HandoffToAgent endpoint is the only public path that promotes an
// agent, and using it from a fixture would couple the seed step to the
// handoff event we are about to assert against.
//
// Returns the task_actors.public_id so callers can assert on it.
func AssignAgentToTask(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string, agentInternalID uint32) string {
	t.Helper()
	require.NotNil(t, db, "AssignAgentToTask requires a *sql.DB")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wsID, taskID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		wsID, taskPublicID,
	).Scan(&taskID))

	actorPub := types.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO task_actors (
			public_id, workspace_id, task_id, agent_id, kind, role, enabled
		) VALUES (?, ?, ?, ?, 'agent', 'assignee', TRUE)
	`, actorPub, wsID, taskID, agentInternalID)
	require.NoError(t, err, "insert task_actors")
	return actorPub.UUID().String()
}

// SetTaskAgentMemo writes the supplied map into tasks.agent_memo via
// JSON_MERGE_PATCH semantics. Used by tests that need to pre-seed
// handoff_count or attempts to drive a specific branch (loop budget,
// autoactions stuck threshold).
func SetTaskAgentMemo(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string, memo map[string]any) {
	t.Helper()
	require.NotNil(t, db, "SetTaskAgentMemo requires a *sql.DB")
	require.NotEmpty(t, memo, "SetTaskAgentMemo requires a non-empty memo map")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := json.Marshal(memo)
	require.NoError(t, err)

	var wsID, taskID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		wsID, taskPublicID,
	).Scan(&taskID))

	_, err = db.ExecContext(ctx,
		`UPDATE tasks SET agent_memo = JSON_MERGE_PATCH(COALESCE(agent_memo, '{}'), CAST(? AS JSON))
		 WHERE workspace_id = ? AND id = ?`,
		string(raw), wsID, taskID,
	)
	require.NoError(t, err, "update tasks.agent_memo")
}

// AgentTaskInternalIDs resolves the internal task id for a given task
// public id. Several handoff assertions need the internal id to query
// events / task_actors by FK; returning it from one place keeps the
// per-test scaffolding tight.
func AgentTaskInternalIDs(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string) (workspaceID uint32, taskID uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&workspaceID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, taskPublicID,
	).Scan(&taskID))
	return workspaceID, taskID
}

// lastInsertID extracts result.LastInsertId() and fails the test on error.
func lastInsertID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// ---- Scripted provider -----------------------------------------------------

// ScriptedProvider is the only fake in the agent task-work tests: an LLM
// provider that answers from a fixed list instead of a network call. Every
// other dependency (database, taskservice, runner, REST handlers) is real.
type ScriptedProvider struct {
	mu         sync.Mutex
	texts      []string
	next       int
	costMicros int64
	route      func(req providers.Request) (string, bool)
	requests   []providers.Request
}

// NewScriptedProvider answers the n-th call with texts[n]. Once the list is
// exhausted every further call answers with a finish move, so an
// unexpected extra call shows up in Calls rather than as a failure here.
func NewScriptedProvider(texts ...string) *ScriptedProvider {
	return &ScriptedProvider{texts: texts}
}

// WithCost makes every response report this cost in millionths of a USD.
func (p *ScriptedProvider) WithCost(micros int64) *ScriptedProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.costMicros = micros
	return p
}

// Route lets a test answer by request (for example by the agent's system
// prompt) before the sequential list is consulted; ok=false falls through.
func (p *ScriptedProvider) Route(fn func(req providers.Request) (string, bool)) *ScriptedProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.route = fn
	return p
}

// Push appends answers to the sequential list.
func (p *ScriptedProvider) Push(texts ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.texts = append(p.texts, texts...)
}

// Calls reports how many completions were requested.
func (p *ScriptedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

// Requests returns a copy of every request received, oldest first.
func (p *ScriptedProvider) Requests() []providers.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providers.Request(nil), p.requests...)
}

// Name implements providers.Provider.
func (p *ScriptedProvider) Name() string { return "scripted" }

// Kind implements providers.Provider.
func (p *ScriptedProvider) Kind() providers.Kind { return providers.KindOpenAICompat }

// Model implements providers.Provider.
func (p *ScriptedProvider) Model() string { return "scripted-model" }

// Complete implements providers.Provider.
func (p *ScriptedProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	text, ok := "", false
	if p.route != nil {
		text, ok = p.route(req)
	}
	if !ok {
		if p.next < len(p.texts) {
			text, p.next = p.texts[p.next], p.next+1
		} else {
			text = Finish("script exhausted")
		}
	}
	mode := providers.OutputModeNone
	if req.Output != nil {
		mode = providers.OutputModeValidatedText
	}
	return &providers.Response{
		Model:       "scripted-model",
		Text:        text,
		Termination: providers.TerminationComplete,
		OutputMode:  mode,
		CostMicros:  p.costMicros,
	}, nil
}

// Move renders the model answer for one task-work step: the tool and its
// arguments in the shape the output contract requires, every unused
// argument null.
func Move(tool string, args map[string]any) string {
	full := map[string]any{
		"body": nil, "priority": nil, "due_on": nil,
		"transition": nil, "title": nil, "summary": nil,
	}
	for k, v := range args {
		full[k] = v
	}
	raw, err := json.Marshal(map[string]any{"thought": "scripted " + tool, "tool": tool, "args": full})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// Finish is the move that ends a run with a summary.
func Finish(summary string) string {
	return Move("finish", map[string]any{"summary": summary})
}

// ---- Agent runtime -----------------------------------------------------------

// AgentEnv is a real orchestrator runner over a real executor, taskservice and
// database, wired to a ScriptedProvider. It stands in for the worker and the
// event hook that cmd/api starts, which the HTTP test server does not run.
type AgentEnv struct {
	DB       *sql.DB
	Provider *ScriptedProvider
	Runner   *agentruntime.OrchestratorRunner
	matcher  agentruntime.OnEventAgentsQuerier
}

// NewAgentEnv builds the runner the way cmd/api does for task work.
func NewAgentEnv(t *testing.T, db *sql.DB, provider *ScriptedProvider) *AgentEnv {
	t.Helper()
	require.NotNil(t, db)
	q := generated.New(db)
	budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
		return q.SumAiCostTodayForWorkspace(ctx, ai.DailyCostParams(ctx, q, wsID))
	})
	exec := &ai.AgentExecutor{
		Queries: q,
		Resolver: ai.AgentProviderResolverFunc(func(context.Context, uint32, types.PublicID) (providers.Provider, error) {
			return provider, nil
		}),
		Guard:       ai.NewCostGuard(budget, 0),
		Log:         router.NewDBInvocationLogger(db, nil),
		TaskService: taskservice.New(db, q, mutationlog.New(db, q)),
		TaskWork:    q,
	}
	return &AgentEnv{
		DB:       db,
		Provider: provider,
		Runner:   &agentruntime.OrchestratorRunner{DB: db, Queries: q, Executor: exec},
		matcher:  agentruntime.NewSqlcOnEventQuerier(db),
	}
}

// TriggeredRun is one run RunTriggered started: the agent, the event that
// woke it, and the error the runner returned (the queue would nack on it).
type TriggeredRun struct {
	AgentID uint32
	EventID uint64
	Err     error
}

// maxTriggerRounds bounds RunTriggered's follow-up passes over events the
// runs themselves wrote.
const maxTriggerRounds = 10

// RunTriggered reproduces the on-event path synchronously: for every event of
// the workspace after sinceEventID it asks the real candidate query which
// agents the event wakes and runs each one through the real runner, then does
// the same for the events those runs wrote, until a pass finds none. An
// (agent, event) pair runs once, as the queue's dedupe key guarantees.
func (e *AgentEnv) RunTriggered(t *testing.T, workspacePublicID string, sinceEventID uint64) []TriggeredRun {
	t.Helper()
	wsID := ResolveWorkspaceInternalID(t, e.DB, workspacePublicID)
	ctx := context.Background()
	type pair struct {
		agent uint32
		event uint64
	}
	seen := map[pair]bool{}
	var runs []TriggeredRun
	cursor := sinceEventID
	for round := 0; round < maxTriggerRounds; round++ {
		ids := eventIDsAfter(t, e.DB, wsID, cursor)
		if len(ids) == 0 {
			return runs
		}
		cursor = ids[len(ids)-1]
		for _, id := range ids {
			matches, err := e.matcher.ListOnEventAgentsForEvent(ctx, wsID, id)
			require.NoError(t, err, "candidate query for event %d", id)
			for _, m := range matches {
				if seen[pair{m.ID, id}] {
					continue
				}
				seen[pair{m.ID, id}] = true
				job := agentruntime.Job{
					AgentID:       m.ID,
					WsID:          m.WorkspaceID,
					SourceEventID: id,
					DedupeKey:     fmt.Sprintf("%d:event:%d", m.ID, id),
				}
				runs = append(runs, TriggeredRun{AgentID: m.ID, EventID: id, Err: e.Runner.Run(ctx, job, time.Now())})
			}
		}
	}
	require.FailNow(t, "agent trigger path did not settle", "events were still being produced after %d passes", maxTriggerRounds)
	return nil
}

// RunJob runs one job through the real runner, as the queue worker would.
func (e *AgentEnv) RunJob(t *testing.T, job agentruntime.Job) error {
	t.Helper()
	return e.Runner.Run(context.Background(), job, time.Now())
}

// CandidateAgents returns the internal ids of the agents the event wakes,
// from the same query the event hook uses.
func (e *AgentEnv) CandidateAgents(t *testing.T, workspaceID uint32, eventID uint64) []uint32 {
	t.Helper()
	matches, err := e.matcher.ListOnEventAgentsForEvent(context.Background(), workspaceID, eventID)
	require.NoError(t, err)
	ids := make([]uint32, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	return ids
}

func eventIDsAfter(t *testing.T, db *sql.DB, wsID uint32, after uint64) []uint64 {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM events WHERE workspace_id = ? AND id > ? ORDER BY id ASC`, wsID, after)
	require.NoError(t, err)
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// ---- Fixtures through the API ------------------------------------------------

// CreateAIModel registers a provider in the tenant's workspace through the
// API and returns the public id of its default model. The provider is never
// called: the runner under test resolves its own ScriptedProvider.
func CreateAIModel(t *testing.T, baseURL string, tt *TestTenant) string {
	t.Helper()
	ws := baseURL + "/workspaces/" + tt.WorkspacePublicID + "/ai"
	var provider struct {
		ID string `json:"id"`
	}
	DoJSON(t, http.MethodPost, ws+"/providers", tt.AccessToken, map[string]any{
		"kind":         "openai_compat",
		"name":         "Scripted " + randomHex(4),
		"baseUrl":      "http://127.0.0.1:1/v1",
		"defaultModel": "scripted-model",
		"apiKey":       "sk-test-agent-fixture-0123456789", //#nosec G101 -- synthetic test fixture, never a real key
	}, &provider)
	require.NotEmpty(t, provider.ID)
	var models struct {
		Models []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"models"`
	}
	DoJSON(t, http.MethodGet, ws+"/providers/"+provider.ID+"/models", tt.AccessToken, nil, &models)
	for _, m := range models.Models {
		if m.IsDefault {
			return m.ID
		}
	}
	require.FailNow(t, "provider has no default model", "models=%+v", models.Models)
	return ""
}

// TaskAgentOptions describes an agent created through POST /ai/agents.
type TaskAgentOptions struct {
	Name         string
	SystemPrompt string
	// ScheduleKind defaults to disabled; task work does not need a schedule.
	ScheduleKind      string
	EventTriggerTypes []string
	// Tools replaces the default grants (every operation on approval) through
	// PATCH .../tools when non-nil; an empty map grants nothing.
	Tools map[string]string
	// MonthlyCostCapCents has no API surface; it is written to the row.
	MonthlyCostCapCents int
}

// TaskAgent is an agent created by CreateTaskAgent.
type TaskAgent struct {
	ID       uint32
	PublicID string
	Name     string
}

// CreateTaskAgent creates an agent through the API with a collision-free name.
func CreateTaskAgent(t *testing.T, db *sql.DB, baseURL string, tt *TestTenant, modelID string, o TaskAgentOptions) *TaskAgent {
	t.Helper()
	if o.Name == "" {
		o.Name = "Agent " + randomHex(5)
	}
	if o.SystemPrompt == "" {
		o.SystemPrompt = "You work on assigned tasks. Marker " + randomHex(5)
	}
	body := map[string]any{"modelId": modelID, "name": o.Name, "systemPrompt": o.SystemPrompt}
	if o.ScheduleKind != "" {
		body["scheduleKind"] = o.ScheduleKind
	}
	if len(o.EventTriggerTypes) > 0 {
		body["eventTriggerTypes"] = o.EventTriggerTypes
	}
	ws := baseURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/agents"
	var created struct {
		ID    string            `json:"id"`
		Tools map[string]string `json:"tools"`
	}
	DoJSON(t, http.MethodPost, ws, tt.AccessToken, body, &created)
	require.NotEmpty(t, created.ID)
	if o.Tools != nil {
		DoJSON(t, http.MethodPatch, ws+"/"+created.ID+"/tools", tt.AccessToken, map[string]any{"tools": o.Tools}, nil)
	}
	var id uint32
	require.NoError(t, db.QueryRow(
		`SELECT id FROM ai_agents WHERE public_id = UUID_TO_BIN(?, 0)`, created.ID).Scan(&id))
	if o.MonthlyCostCapCents > 0 {
		_, err := db.Exec(`UPDATE ai_agents SET monthly_cost_cap_cents = ? WHERE id = ?`, o.MonthlyCostCapCents, id)
		require.NoError(t, err)
	}
	return &TaskAgent{ID: id, PublicID: created.ID, Name: o.Name}
}

// GrantAll returns grants for every task operation in one mode.
func GrantAll(mode string) map[string]string {
	return map[string]string{
		"add_comment": mode, "set_priority": mode, "set_due_on": mode,
		"transition_task": mode, "create_subtask": mode,
	}
}

// ---- Event, invocation and audit reads ---------------------------------------

// EventRecord is one row of the events table as the tests read it.
type EventRecord struct {
	ID           uint64
	PublicID     string
	Type         string
	TaskID       uint32
	ActorUserID  uint32
	ActorAgentID uint32
	Payload      map[string]any
}

// LatestEventID returns the highest events.id of the workspace, 0 when none.
func LatestEventID(t *testing.T, db *sql.DB, workspaceID uint32) uint64 {
	t.Helper()
	var id sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT MAX(id) FROM events WHERE workspace_id = ?`, workspaceID).Scan(&id))
	if !id.Valid {
		return 0
	}
	return uint64(id.Int64) //#nosec G115 -- MAX(id) is non-negative
}

// EventsOfType returns the workspace's events of one type after afterID, in
// id order. taskID 0 matches every task.
func EventsOfType(t *testing.T, db *sql.DB, workspaceID, taskID uint32, eventType string, afterID uint64) []EventRecord {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, BIN_TO_UUID(public_id, 0), type, COALESCE(task_id, 0),
		       COALESCE(actor_user_id, 0), COALESCE(actor_agent_id, 0), CAST(payload_json AS CHAR)
		FROM events
		WHERE workspace_id = ? AND type = ? AND id > ? AND (? = 0 OR task_id = ?)
		ORDER BY id ASC`, workspaceID, eventType, afterID, taskID, taskID)
	require.NoError(t, err)
	defer rows.Close()
	var out []EventRecord
	for rows.Next() {
		var r EventRecord
		var payload string
		require.NoError(t, rows.Scan(&r.ID, &r.PublicID, &r.Type, &r.TaskID, &r.ActorUserID, &r.ActorAgentID, &payload))
		require.NoError(t, json.Unmarshal([]byte(payload), &r.Payload))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// InsertEvent writes one event row directly. It exists for the cases no API
// can produce: an event with no actor, or a payload shape no handler emits.
func InsertEvent(t *testing.T, db *sql.DB, workspaceID, taskID, actorUserID, actorAgentID uint32, eventType string, payload map[string]any) uint64 {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	nullable := func(v uint32) sql.NullInt64 { return sql.NullInt64{Int64: int64(v), Valid: v != 0} }
	res, err := db.Exec(`
		INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, actor_agent_id, type, payload_json, occurred_at)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, ?, ?, CAST(? AS JSON), NOW(3))`,
		workspaceID, nullable(taskID), nullable(actorUserID), nullable(actorAgentID), eventType, string(raw))
	require.NoError(t, err)
	return uint64(lastInsertID(t, res)) //#nosec G115 -- AUTO_INCREMENT ids are non-negative
}

// CountInvocations counts the workspace's ai_invocations rows of one purpose.
func CountInvocations(t *testing.T, db *sql.DB, workspaceID uint32, purpose string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM ai_invocations WHERE workspace_id = ? AND purpose = ?`, workspaceID, purpose).Scan(&n))
	return n
}

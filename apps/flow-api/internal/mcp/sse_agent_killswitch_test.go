package mcp_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// agentStreamFixture is one workspace member holding one MCP token that
// acts on behalf of an AI agent in the same workspace.
type agentStreamFixture struct {
	*mcpStreamFixture
	agent *helpers.SeededAgent
}

// Values for ai_agents.allowed_scopes_json.
//
// scopesInheritFromToken is NULL, the column's documented "inherit from
// the token" state and the only value any code path in the product ever
// produces — no INSERT or UPDATE anywhere writes this column.
// scopesReadWorkspace is an explicit list, which is the shape the guard's
// scope rule was written against.
const (
	scopesInheritFromToken = ""
	scopesReadWorkspace    = `["read:workspace"]`
	scopesWriteOnly        = `["write:workspace"]`
)

// seedAgentBackedStream builds a workspace, a member, an agent, and an MCP
// token bound to that agent. Every case gets its own workspace so the
// suite stays parallel.
func seedAgentBackedStream(
	t *testing.T, db *sql.DB, opts helpers.SeedAgentOptions, allowedScopes string,
) *agentStreamFixture {
	t.Helper()
	fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})

	var wsPublicID string
	require.NoError(t, db.QueryRow(
		`SELECT BIN_TO_UUID(public_id, 0) FROM workspaces WHERE id = ?`, fx.wsID,
	).Scan(&wsPublicID))

	agent := helpers.SeedAgent(t, db, wsPublicID, opts)

	if allowedScopes != scopesInheritFromToken {
		_, err := db.Exec(
			`UPDATE ai_agents SET allowed_scopes_json = CAST(? AS JSON) WHERE id = ?`,
			allowedScopes, agent.AgentID)
		require.NoError(t, err)
	}

	// The token is what carries the agent identity onto the transport;
	// mcp_tokens.agent_id is the only place the stream can learn it from.
	res, err := db.Exec(`UPDATE mcp_tokens SET agent_id = ? WHERE id = ?`, agent.AgentID, fx.tokenID)
	require.NoError(t, err)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, affected, "the token must end up bound to the seeded agent")

	return &agentStreamFixture{mcpStreamFixture: fx, agent: agent}
}

// requireStreamRefusal asserts err is a refusal carrying the given stable
// error code, rather than an infrastructure failure that happens to be
// non-nil.
func requireStreamRefusal(t *testing.T, err error, spec *apierrors.Spec) {
	t.Helper()
	require.Error(t, err)
	var ae *apierrors.APIError
	require.True(t, stderrors.As(err, &ae), "expected an API error, got %v", err)
	require.NotNil(t, ae.Spec)
	require.Equal(t, spec.Code, ae.Spec.Code)
}

// TestMCPStreamHonorsTheAgentKillSwitch proves that disabling or pausing an
// AI agent reaches the event stream its token has already opened, and that
// it reaches nothing else.
//
// Both switches stopped the agent's tool calls on the POST path. The stream
// was the one place they did not reach: a paused agent kept receiving every
// event in the workspace for as long as it stayed connected, which is the
// whole workspace, continuously, from a credential an operator believed
// they had switched off.
func TestMCPStreamHonorsTheAgentKillSwitch(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB

	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ctx := context.Background()

	t.Run("paused_agent/open_stream_closes", func(t *testing.T) {
		t.Parallel()
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)
		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})

		// Positive control. Without it the refusal below is also what an
		// implementation that refuses every agent-backed stream produces.
		require.NoError(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess),
			"a healthy agent's stream must survive revalidation")

		_, err := db.Exec(`UPDATE ai_agents SET paused = TRUE WHERE id = ?`, fx.agent.AgentID)
		require.NoError(t, err)

		requireStreamRefusal(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess), apierrors.AiAgentPaused)
	})

	t.Run("disabled_agent/open_stream_closes", func(t *testing.T) {
		t.Parallel()
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)
		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})

		require.NoError(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess),
			"a healthy agent's stream must survive revalidation")

		_, err := db.Exec(`UPDATE ai_agents SET enabled = FALSE WHERE id = ?`, fx.agent.AgentID)
		require.NoError(t, err)

		requireStreamRefusal(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess), apierrors.AiAgentPaused)
	})

	t.Run("revalidation/rebuilt_session_keeps_the_agent_id", func(t *testing.T) {
		t.Parallel()
		// Revalidation does not reuse the session it was handed; it rebuilds
		// one from the token row and holds *that* to the guard. If the rebuilt
		// session loses the agent id, the guard is skipped for a zero id and
		// every tick passes, which is indistinguishable from not running it.
		//
		// The caller's session is deliberately given a different, healthy
		// agent's id. The token is bound to a paused agent, so a refusal can
		// only come from an agent id the rebuild read out of the database.
		paused := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)
		healthy := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)

		_, err := db.Exec(`UPDATE ai_agents SET paused = TRUE WHERE id = ?`, paused.agent.AgentID)
		require.NoError(t, err)

		stale := mcp.NewTestAgentSession(
			paused.userID, paused.wsID, healthy.agent.AgentID, []string{"read:workspace"})

		requireStreamRefusal(t,
			handler.RevalidateStreamForTest(ctx, paused.plain, stale), apierrors.AiAgentPaused)
	})

	t.Run("healthy_agent/stream_survives_repeated_revalidation", func(t *testing.T) {
		t.Parallel()
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)
		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})

		for i := 0; i < 5; i++ {
			require.NoErrorf(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess),
				"an enabled, unpaused agent must keep its stream across ticks (tick %d)", i+1)
		}
	})

	t.Run("human_token/guard_does_not_apply", func(t *testing.T) {
		t.Parallel()
		// A human token's row carries a NULL agent_id, so the rebuilt session
		// carries a zero one and the guard is skipped. That skip is what this
		// asserts: an implementation that ran the guard unconditionally would
		// look up agent id zero, find no row, read it as a disabled agent and
		// close the stream of every human client in the deployment.
		fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
		sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"read:workspace"})

		require.NoError(t, handler.AuthorizeStreamForTest(ctx, sess))
		require.NoError(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess))
	})

	t.Run("over_budget_agent/keeps_its_stream", func(t *testing.T) {
		t.Parallel()
		// A stream delivers events and spends no provider money, so the
		// monthly cost cap has nothing to bound on it. Only the access half
		// of the guard runs there.
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{MonthlyCostCapCents: 100}, scopesReadWorkspace)
		recordAgentSpend(t, db, fx, 50.00)

		// Positive control on the spend itself: the POST path, which does
		// apply the cap, refuses this agent. Without it "the stream stayed
		// open" would also be what an agent that was never over budget
		// produces.
		status, body := postToolCall(t, srv.URL, fx.plain, "get_task", map[string]any{
			"taskId": uuid.Must(uuid.NewV7()).String(),
		})
		// A refusal the dispatcher reaches after the envelope is parsed rides
		// in the JSON-RPC error body, not the HTTP status.
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, apierrors.AiCostGuardExceeded.Code, toolCallErrorCode(t, body),
			"the seeded spend must actually put the agent over its cap")

		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})
		require.NoError(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess),
			"a spend cap governs money, and a stream spends none")
	})

	t.Run("paused_agent/cannot_open_a_stream", func(t *testing.T) {
		t.Parallel()
		// Opening is synchronous, so this refusal can still be an HTTP status:
		// it happens before the SSE headers go out. Once they have, the only
		// way left to say anything is the stream.closed frame.
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesReadWorkspace)
		_, err := db.Exec(`UPDATE ai_agents SET paused = TRUE WHERE id = ?`, fx.agent.AgentID)
		require.NoError(t, err)

		status, body := getStream(t, srv.URL, fx.plain)
		require.Equal(t, apierrors.AiAgentPaused.Status, status)
		require.Equal(t, apierrors.AiAgentPaused.Code, toolCallErrorCode(t, body))
	})

	t.Run("agent_with_the_columns_creation_leaves/open_stream_closes", func(t *testing.T) {
		t.Parallel()
		// The fixture here is deliberately the untouched one: nothing is
		// stamped into allowed_scopes_json, so the row is exactly what
		// CreateAgent leaves behind. Every other case in this file writes an
		// explicit ["read:workspace"], and that is how seven green cases came
		// to sit on top of a projection that failed for every agent a
		// deployment actually has.
		//
		// The kill switch has to reach an agent in that shape, because that
		// is the only shape an agent is ever created in.
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesInheritFromToken)
		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})

		require.NoError(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess),
			"a healthy agent's stream must survive revalidation")

		_, err := db.Exec(`UPDATE ai_agents SET paused = TRUE WHERE id = ?`, fx.agent.AgentID)
		require.NoError(t, err)

		requireStreamRefusal(t, handler.RevalidateStreamForTest(ctx, fx.plain, sess), apierrors.AiAgentPaused)
	})

	t.Run("agent_scoped_away_from_reads/open_stream_closes", func(t *testing.T) {
		t.Parallel()
		// The guard's third rule. An agent allowed only write:workspace may
		// not hold a stream, which is a read, even though the token it rides
		// on carries read:workspace — the narrower of the two governs.
		//
		// No agent in a running deployment is in this shape: nothing writes
		// allowed_scopes_json, so the list reaching the guard is always empty
		// and the rule never fires. It is pinned here so the rule is not
		// quietly lost before the column gets a writer, and so the refusal is
		// named as a scope refusal rather than as the kill switch.
		fx := seedAgentBackedStream(t, db, helpers.SeedAgentOptions{}, scopesWriteOnly)
		sess := mcp.NewTestAgentSession(fx.userID, fx.wsID, fx.agent.AgentID, []string{"read:workspace"})

		requireStreamRefusal(t,
			handler.RevalidateStreamForTest(ctx, fx.plain, sess), apierrors.McpScopeInsufficient)
	})
}

// getStream issues the SSE GET and reads the whole response. It is for the
// refusal cases only: a stream that is allowed to open never ends, so the
// read would not return.
func getStream(t *testing.T, baseURL, token string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/mcp", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		// The stream opened, so the body never ends. Hand the status back and
		// let the caller's assertion name what went wrong instead of waiting
		// out the read deadline.
		return resp.StatusCode, nil
	}
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// recordAgentSpend books an ai_invocations row against the fixture's agent
// for the current month, which is the window the dispatch guard sums.
func recordAgentSpend(t *testing.T, db *sql.DB, fx *agentStreamFixture, costUSD float64) {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	_, err := db.Exec(`
		INSERT INTO ai_invocations (
			public_id, workspace_id, provider_id, agent_id,
			purpose, model, prompt_redacted, response_redacted,
			cost_estimate, status, invoked_at
		) VALUES (?, ?, ?, ?, 'test', 'test-model', '[redacted]', '[redacted]', ?, 'ok', ?)
	`, pub[:], fx.wsID, fx.agent.ProviderID, fx.agent.AgentID, costUSD, time.Now().UTC())
	require.NoError(t, err)
}

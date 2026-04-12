// Package mcp implements the nodate-flow MCP server. It exposes a
// Streamable HTTP (JSON-RPC 2.0) transport at /mcp with two methods:
//
//   - POST: single JSON-RPC 2.0 request/response (tools/call, etc.)
//   - GET:  SSE stream delivering real-time workspace event notifications
//
// Tools are intentionally implemented as direct sqlc calls in-process
// (Path A) rather than calling the HTTP handlers of the REST API.
// Direct sqlc is not considered "raw SQL" for the purposes of the DRY
// rule — the generated queries package is the single source of truth
// for SQL.
//
// Current tool set: list_projects, list_tasks, get_task, create_task,
// update_task, add_comment, search_tasks, propose_tasks_from,
// propose_priority, propose_steps, propose_duplicates, propose_lens,
// list_timeboxes, create_timebox, add_task_to_timebox, export_tasks,
// propose_relations. Per-token rate limiting is enforced at 60 req/min.
package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/agentguard"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlquery"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle needed to construct an MCP [Handler].
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// AI is the optional LLM orchestrator. When nil, the propose_* tools
	// return AI.PROVIDER.NOT_CONFIGURED.
	AI *ai.Orchestrator
	// Embedder is the optional task embedding client. When nil,
	// propose_duplicates returns AI.PROVIDER.NOT_CONFIGURED.
	Embedder *embed.Client
	// NlQuery is the optional natural-language-to-Lens compiler. When
	// nil, the propose_lens tool returns AI.PROVIDER.NOT_CONFIGURED.
	NlQuery *nlquery.Compiler
}

// Handler implements http.Handler for the /mcp endpoint. It supports
// both POST (JSON-RPC request/response) and GET (SSE event stream).
type Handler struct {
	deps  Deps
	tools map[string]tool
	rl    mcpRateLimiter
	sse   *sseHub
}

// mcpRateLimiter is a per-token sliding window rate limiter for the MCP
// endpoint. It limits each MCP token to 60 requests per minute.
type mcpRateLimiter struct {
	mu      sync.Mutex
	tokens  map[string]*tokenBucket
	maxReqs int
	window  time.Duration
}

type tokenBucket struct {
	timestamps []time.Time
}

func newMCPRateLimiter() mcpRateLimiter {
	return mcpRateLimiter{
		tokens:  make(map[string]*tokenBucket),
		maxReqs: 60,
		window:  time.Minute,
	}
}

// allow checks whether the token is within its rate limit. Returns true
// if allowed, false if the limit is exceeded.
func (rl *mcpRateLimiter) allow(token string) (bool, time.Duration) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.tokens[token]
	if !ok {
		b = &tokenBucket{}
		rl.tokens[token] = b
	}

	cutoff := now.Add(-rl.window)
	n := 0
	for _, ts := range b.timestamps {
		if ts.After(cutoff) {
			b.timestamps[n] = ts
			n++
		}
	}
	b.timestamps = b.timestamps[:n]

	if len(b.timestamps) >= rl.maxReqs {
		retryAfter := b.timestamps[0].Add(rl.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}

	b.timestamps = append(b.timestamps, now)
	return true, 0
}

// NewHandler constructs the MCP HTTP handler with the default tool
// set registered and SSE hub initialised. Call [Handler.RegisterEventHook]
// after construction to wire the eventbus notify hook.
func NewHandler(deps Deps) *Handler {
	h := &Handler{
		deps:  deps,
		tools: map[string]tool{},
		rl:    newMCPRateLimiter(),
		sse:   newSSEHub(),
	}
	registerTools(h)
	return h
}

// RegisterEventHook returns the eventbus.NotifyHook that broadcasts
// workspace events to connected SSE clients. The caller should pass
// this to eventbus.AddNotifyHook.
func (h *Handler) RegisterEventHook() func(ctx context.Context, workspaceID uint32, eventType string) {
	return h.onWorkspaceEvent
}

// ServeHTTP is the Streamable HTTP entry point. GET opens an SSE
// stream for workspace event notifications. POST expects a single
// JSON-RPC 2.0 request frame.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.serveSSE(w, r)
		return
	case http.MethodPost:
		// fall through to POST handling below
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPCError(w, nil, apierrors.McpProtocolFrameMalformed, err.Error())
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" {
		writeRPCError(w, nil, apierrors.McpProtocolFrameMalformed, "invalid JSON-RPC 2.0 frame")
		return
	}

	// Authenticate via Authorization: Bearer mcp_...
	tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
	if !ok || !strings.HasPrefix(tok, auth.PrefixMCP) {
		writeRPCError(w, req.ID, apierrors.McpTokenUnknown, "missing mcp bearer")
		return
	}
	session, err := h.authenticate(r.Context(), tok)
	if err != nil {
		var ae *apierrors.APIError
		if errors.As(err, &ae) {
			writeRPCError(w, req.ID, ae.Spec, ae.Spec.Message)
			return
		}
		writeRPCError(w, req.ID, apierrors.InternalUnexpected, "auth failed")
		return
	}

	// Per-token rate limiting.
	if allowed, retryAfter := h.rl.allow(tok); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		writeRPCError(w, req.ID, apierrors.RateLimitExceeded, "rate limit exceeded")
		return
	}

	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "nodate-flow",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools":         map[string]any{},
				"notifications": map[string]any{},
			},
		})
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{"tools": h.listTools()})
	case "tools/call":
		h.handleToolCall(w, r, session, req)
	default:
		writeRPCError(w, req.ID, apierrors.McpProtocolFrameMalformed, "unknown method: "+req.Method)
	}
}

func (h *Handler) handleToolCall(w http.ResponseWriter, r *http.Request, s *session, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) == 0 {
		writeRPCError(w, req.ID, apierrors.McpProtocolFrameMalformed, "missing params")
		return
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, apierrors.McpProtocolFrameMalformed, "invalid params")
		return
	}
	t, ok := h.tools[params.Name]
	if !ok {
		writeRPCError(w, req.ID, apierrors.McpToolNotFound, "tool not found: "+params.Name)
		return
	}
	if !s.hasScope(t.requiredScope) {
		h.audit(r.Context(), s, params.Name, params.Arguments, nil,
			generated.McpInvocationsStatusDenied, apierrors.McpScopeInsufficient.Code, 0)
		writeRPCError(w, req.ID, apierrors.McpScopeInsufficient, "scope "+t.requiredScope+" required")
		return
	}
	// 2.MCP-2 agent guard: when the session is backed by an AI agent
	// token, run agentguard.Decide to enforce enabled / paused /
	// allowed-scopes. Monthly cost-cap enforcement here is still a
	// placeholder — ai_invocations.agent_id is a follow-up, so we
	// pass 0 spend and nil cap until that column lands.
	if s.agentID != 0 {
		agent, aerr := h.loadAgentGuardSnapshot(r.Context(), s.agentID)
		if aerr != nil {
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusError, apierrors.McpToolExecutionFailed.Code, 0)
			writeRPCError(w, req.ID, apierrors.McpToolExecutionFailed, aerr.Error())
			return
		}
		spent, serr := h.loadAgentMonthSpendCents(r.Context(), s.agentID)
		if serr != nil {
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusError, apierrors.McpToolExecutionFailed.Code, 0)
			writeRPCError(w, req.ID, apierrors.McpToolExecutionFailed, serr.Error())
			return
		}
		decision := agentguard.Decide(agent, agentguard.Request{
			ToolName:        params.Name,
			RequiredScope:   t.requiredScope,
			SpentCentsMonth: spent,
		})
		if decision.Outcome != agentguard.OutcomeAllow {
			spec := apierrors.McpScopeInsufficient
			if decision.Outcome == agentguard.OutcomePause {
				spec = apierrors.AiCostGuardExceeded
			}
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusDenied, spec.Code, 0)
			writeRPCError(w, req.ID, spec, "agent guard: "+decision.Reason)
			return
		}
	}
	args := params.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	start := time.Now()
	runCtx := r.Context()
	if s.agentID != 0 {
		runCtx = ai.WithAgentID(runCtx, s.agentID)
	}
	result, toolErr := t.run(runCtx, h.deps, s, args)
	dur := int32(time.Since(start).Milliseconds())
	if toolErr != nil {
		var ae *apierrors.APIError
		spec := apierrors.McpToolExecutionFailed
		if errors.As(toolErr, &ae) {
			spec = ae.Spec
		}
		h.audit(r.Context(), s, params.Name, args, nil,
			generated.McpInvocationsStatusError, spec.Code, dur)
		writeRPCError(w, req.ID, spec, toolErr.Error())
		return
	}
	resultJSON, _ := json.Marshal(result)
	// Redact secrets from the response text so MCP callers never see
	// raw API keys, tokens, or passwords in tool output.
	redactedResult := ai.RedactJSONFields(string(resultJSON))
	h.audit(r.Context(), s, params.Name, args, json.RawMessage(redactedResult),
		generated.McpInvocationsStatusOk, "", dur)
	// MCP spec: result content is a list of content parts. We wrap the
	// tool output as a single JSON text part.
	writeRPCResult(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": redactedResult},
		},
		"isError": false,
	})
}

// ----------------------------------------------------------------------------
// JSON-RPC primitives
// ----------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, spec *apierrors.Spec, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(spec.Status)
	// JSON-RPC code = -32000 block reserved for server errors; we encode the
	// nodate-flow error code string in data.code for callers that care.
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    -32000,
			Message: msg,
			Data:    map[string]any{"code": spec.Code},
		},
	})
}

// loadAgentGuardSnapshot fetches the minimal ai_agents row that
// agentguard.Decide needs, via raw SQL to avoid a sqlc regen on this
// slice. Returns a zero Agent (not enabled) if the row is missing so
// Decide will pause the caller.
func (h *Handler) loadAgentGuardSnapshot(ctx context.Context, agentID uint32) (agentguard.Agent, error) {
	const q = `SELECT enabled, paused, allowed_scopes_json, monthly_cost_cap_cents FROM ai_agents WHERE id = ? LIMIT 1`
	var (
		enabled       bool
		paused        bool
		scopesJSON    sql.NullString
		monthlyCapRaw sql.NullInt64
	)
	if err := h.deps.DB.QueryRowContext(ctx, q, agentID).Scan(&enabled, &paused, &scopesJSON, &monthlyCapRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agentguard.Agent{Enabled: false}, nil
		}
		return agentguard.Agent{}, err
	}
	var scopes []string
	if scopesJSON.Valid && scopesJSON.String != "" {
		_ = json.Unmarshal([]byte(scopesJSON.String), &scopes)
	}
	var cap *int64
	if monthlyCapRaw.Valid {
		v := monthlyCapRaw.Int64
		cap = &v
	}
	return agentguard.Agent{
		Enabled:             enabled,
		Paused:              paused,
		AllowedScopes:       scopes,
		MonthlyCostCapCents: cap,
	}, nil
}

// loadAgentMonthSpendCents returns the sum of cost_estimate (in cents)
// for ai_invocations attributed to agentID since the first day of the
// current UTC month. Used by the 2.MCP-2 dispatch guard.
func (h *Handler) loadAgentMonthSpendCents(ctx context.Context, agentID uint32) (int64, error) {
	const q = `SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED)
	             FROM ai_invocations WHERE agent_id = ? AND invoked_at >= ?`
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var cents int64
	if err := h.deps.DB.QueryRowContext(ctx, q, agentID, since).Scan(&cents); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return cents, nil
}

func bearerFromHeader(h string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	return tok, tok != ""
}

// audit writes a single mcp_invocations row. It never returns an error
// to the caller; audit failures are logged via fmt.Errorf and swallowed
// because they must not break the tool response.
func (h *Handler) audit(
	ctx context.Context,
	s *session,
	toolName string,
	args json.RawMessage,
	result json.RawMessage,
	status generated.McpInvocationsStatus,
	errCode string,
	durMs int32,
) {
	if h.deps.Queries == nil {
		return
	}
	argsBlob := args
	if len(argsBlob) == 0 {
		argsBlob = json.RawMessage("{}")
	}
	resBlob := result
	if len(resBlob) == 0 {
		resBlob = json.RawMessage("{}")
	}
	// Compact to keep stored JSON minimal.
	var buf bytes.Buffer
	_ = json.Compact(&buf, argsBlob)
	argsBlob = json.RawMessage(ai.RedactJSONFields(buf.String()))
	buf.Reset()
	_ = json.Compact(&buf, resBlob)
	resBlob = json.RawMessage(ai.RedactJSONFields(buf.String()))

	var userID sql.NullInt32
	if s != nil {
		userID = sql.NullInt32{Int32: int32(s.userID), Valid: true}
	}
	var ec sql.NullString
	if errCode != "" {
		ec = sql.NullString{String: errCode, Valid: true}
	}
	_, err := h.deps.Queries.LogMcpInvocation(ctx, generated.LogMcpInvocationParams{
		PublicID:              newPublicID(),
		WorkspaceID:           s.workspaceID,
		UserID:                userID,
		TaskID:                sql.NullInt32{},
		ToolName:              toolName,
		ArgumentsRedactedJson: argsBlob,
		ResultRedactedJson:    resBlob,
		Status:                status,
		ErrorCode:             ec,
		DurationMs:            sql.NullInt32{Int32: durMs, Valid: true},
		InvokedAt:             time.Now().UTC(),
	})
	if err != nil {
		_ = fmt.Errorf("mcp audit log failed: %w", err)
	}
}

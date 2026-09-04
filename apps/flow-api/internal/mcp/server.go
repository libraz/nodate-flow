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
// propose_relations, smart_create_task, list_import_jobs,
// create_import_job. Per-token rate limiting is enforced at 60 req/min.
package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentguard"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// Deps is the dependency bundle needed to construct an MCP [Handler].
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// CalendarQueries is the dedicated sqlc subpackage handle that emits
	// every calendar-domain query. Calendar-aware MCP tools reach for it
	// instead of [Queries] for any *_calendar_*, *_event_*, *_invite_*,
	// *_attendee_*, *_memo_* operation.
	CalendarQueries *calendar.Queries
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
	mu        sync.Mutex
	tokens    map[string]*tokenBucket
	maxReqs   int
	window    time.Duration
	lastEvict time.Time
}

type tokenBucket struct {
	timestamps []time.Time
	// throttling records that this bucket has already refused a request
	// and has not admitted one since. It exists so the audit trail gets
	// one row per throttling episode instead of one per refused request:
	// a client that keeps firing after it is capped is precisely the
	// client that would otherwise turn a cheap in-memory refusal into an
	// unbounded stream of INSERTs, which is a worse outcome than the
	// missing record. It clears on the next admitted request, so a token
	// that trips the limiter again tomorrow is recorded again.
	throttling bool
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
//
// firstRefusal is true only on the refusal that begins a throttling
// episode — the caller uses it to decide whether the refusal is worth an
// audit row. See [tokenBucket.throttling].
func (rl *mcpRateLimiter) allow(token string) (allowed bool, retryAfter time.Duration, firstRefusal bool) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Periodically evict stale entries to prevent unbounded map growth.
	if now.Sub(rl.lastEvict) > rl.window*2 {
		cutoff := now.Add(-rl.window)
		for k, v := range rl.tokens {
			if len(v.timestamps) == 0 || v.timestamps[len(v.timestamps)-1].Before(cutoff) {
				delete(rl.tokens, k)
			}
		}
		rl.lastEvict = now
	}

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
		wait := b.timestamps[0].Add(rl.window).Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		first := !b.throttling
		b.throttling = true
		return false, wait, first
	}

	b.timestamps = append(b.timestamps, now)
	b.throttling = false
	return true, 0, false
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
func (h *Handler) RegisterEventHook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
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
		writeRPCTransportError(w, nil, apierrors.McpProtocolFrameMalformed, "failed to read request body")
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" {
		writeRPCTransportError(w, nil, apierrors.McpProtocolFrameMalformed, "invalid JSON-RPC 2.0 frame")
		return
	}

	// Authenticate via Authorization: Bearer mcp_...
	tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
	if !ok || !strings.HasPrefix(tok, auth.PrefixMCP) {
		writeRPCTransportError(w, req.ID, apierrors.McpTokenUnknown, "missing mcp bearer")
		return
	}
	session, err := h.authenticate(r.Context(), tok)
	if err != nil {
		var ae *apierrors.APIError
		if errors.As(err, &ae) {
			// session is non-nil exactly when the token resolved to a
			// workspace but was refused anyway (expiry); that is the
			// refusal the audit trail has to carry.
			h.auditTransportRefusal(r.Context(), session, ae.Spec)
			writeRPCTransportError(w, req.ID, ae.Spec, ae.Spec.Message)
			return
		}
		writeRPCTransportError(w, req.ID, apierrors.InternalUnexpected, "auth failed")
		return
	}

	// Per-token rate limiting. Hash the token so the plaintext is never
	// stored as a map key in the rate limiter.
	tokHash := hashToken(tok)
	if allowed, retryAfter, first := h.rl.allow(tokHash); !allowed {
		if first {
			h.auditTransportRefusal(r.Context(), session, apierrors.RateLimitExceeded)
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		writeRPCTransportError(w, req.ID, apierrors.RateLimitExceeded, "rate limit exceeded")
		return
	}

	if len(req.ID) == 0 && req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusNoContent)
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
		writeRPCAppError(w, req.ID, apierrors.McpProtocolFrameMalformed, "unknown method: "+req.Method)
	}
}

func (h *Handler) handleToolCall(w http.ResponseWriter, r *http.Request, s *session, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) == 0 {
		h.audit(r.Context(), s, "", nil, nil,
			generated.McpInvocationsStatusError, apierrors.McpProtocolFrameMalformed.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpProtocolFrameMalformed, "missing params")
		return
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		h.audit(r.Context(), s, "", req.Params, nil,
			generated.McpInvocationsStatusError, apierrors.McpProtocolFrameMalformed.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpProtocolFrameMalformed, "invalid params")
		return
	}
	t, ok := h.tools[params.Name]
	if !ok {
		h.audit(r.Context(), s, params.Name, params.Arguments, nil,
			generated.McpInvocationsStatusError, apierrors.McpToolNotFound.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpToolNotFound, "tool not found: "+params.Name)
		return
	}
	// Scope gate. By design this is a coarse read/write tier check, not
	// a per-tool or per-resource capability. Each tool declares a single
	// requiredScope drawn from the fixed vocabulary in [SupportedScopes]
	// (read:workspace / write:workspace — see [session.hasScope]); a
	// token's granted scopes are matched against it with write-implies-read
	// widening. There is intentionally no scope like "read:calendar" or
	// "write:task:complete": a token that can write the workspace can
	// invoke every mutating tool in it.
	//
	// The finer-grained guarantees the product actually relies on —
	// tenant isolation and per-resource ownership — are enforced
	// downstream, not by this scope check: every tool re-resolves its
	// target via the workspace-scoped resolvers in acl.go
	// (resolveTask / resolveCalendar / resolveWorkspaceUser, …) and the
	// agentguard branch below caps agent-backed tokens. Tightening this
	// to per-tool least privilege would mean expanding the scope
	// vocabulary and the token-issuance UI; that is a deliberate future
	// step, not a gap that weakens the current isolation model. Keep new
	// tools mapped onto the existing read/write tiers until that lands.
	if !s.hasScope(t.requiredScope) {
		h.audit(r.Context(), s, params.Name, params.Arguments, nil,
			generated.McpInvocationsStatusDenied, apierrors.McpScopeInsufficient.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpScopeInsufficient, "scope "+t.requiredScope+" required")
		return
	}
	// Agent guard: when the session is backed by an AI agent token,
	// run agentguard.Decide to enforce enabled / paused /
	// allowed-scopes and the monthly cost cap.
	//
	// The guard answers two different questions here: whether the agent
	// may act at all (enabled / paused / allowed scopes) and whether it
	// may spend (the monthly cost cap). Both refuse with the same
	// Outcome, so the refusal is named from decision.Cause instead: an
	// operator who flips the kill switch must not see the agent's client
	// report a budget failure and go looking at spending limits for it.
	// AI.AGENT.PAUSED is what the SSE stream returns for the same two
	// rules, so both transports name the kill switch identically.
	//
	// Monthly cost-cap enforcement is a deliberate SOFT cap with
	// post-hoc + 95%-margin semantics, not a hard pre-call reserve:
	//   - spend is real: loadAgentMonthSpendCents sums
	//     ai_invocations.cost_estimate for this agent_id this month
	//     (the agent_id column already lands, despite older comments);
	//   - we pass EstimatedCents = 0 because there is no per-tool cost
	//     model yet, so an in-flight call's cost is unknown until it
	//     returns. agentguard therefore pauses the agent the moment
	//     recorded spend reaches 95% of the cap (SpentCentsMonth >=
	//     effectiveCap), bounding worst-case overrun to roughly one
	//     tool call beyond the cap.
	// The would-exceed branch inside agentguard.Decide stays live for
	// future callers that can supply a real EstimatedCents; at this
	// call site it is intentionally inert (0 estimate) until a per-tool
	// estimate lands. Do not fabricate a constant estimate here: a wrong
	// guess would spuriously pause agents well under their real spend.
	if s.agentID != 0 {
		agent, aerr := h.loadAgentGuardSnapshot(r.Context(), s.agentID)
		if aerr != nil {
			slog.ErrorContext(r.Context(), "mcp: agent guard load failed", slog.Any("err", aerr))
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusError, apierrors.McpToolGuardUnavailable.Code, 0)
			writeRPCAppError(w, req.ID, apierrors.McpToolGuardUnavailable, "agent guard check failed")
			return
		}
		spent, serr := h.loadAgentMonthSpendCents(r.Context(), s.agentID)
		if serr != nil {
			slog.ErrorContext(r.Context(), "mcp: agent spend load failed", slog.Any("err", serr))
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusError, apierrors.McpToolGuardUnavailable.Code, 0)
			writeRPCAppError(w, req.ID, apierrors.McpToolGuardUnavailable, "agent spend check failed")
			return
		}
		decision := agentguard.Decide(agent, agentguard.Request{
			ToolName:        params.Name,
			RequiredScope:   t.requiredScope,
			SpentCentsMonth: spent,
			// Soft cap: no per-tool estimate yet, so the would-exceed
			// branch is intentionally inert here and the cap bites
			// post-hoc at 95% of the limit. See the comment above.
			EstimatedCents: 0,
		})
		if decision.Outcome != agentguard.OutcomeAllow {
			var spec *apierrors.Spec
			switch decision.Cause {
			case agentguard.CauseDisabled, agentguard.CausePaused:
				spec = apierrors.AiAgentPaused
			case agentguard.CauseCostCapExhausted, agentguard.CauseCostCapWouldExceed:
				spec = apierrors.AiCostGuardExceeded
			case agentguard.CauseScopeNotAllowed:
				spec = apierrors.McpScopeInsufficient
			case agentguard.CauseNone:
				// CauseNone belongs to an allow, so it cannot reach here;
				// the branch keeps the switch total so a cause added to
				// agentguard still refuses the call rather than falling
				// through to a nil spec.
				spec = apierrors.McpScopeInsufficient
			default:
				spec = apierrors.McpScopeInsufficient
			}
			h.audit(r.Context(), s, params.Name, params.Arguments, nil,
				generated.McpInvocationsStatusDenied, spec.Code, 0)
			writeRPCAppError(w, req.ID, spec, "agent guard: "+decision.Reason)
			return
		}
	}
	// Validate individual argument sizes before execution to prevent
	// excessively large payloads from reaching tool handlers.
	if valErr := validateToolArgs(params.Arguments); valErr != nil {
		h.audit(r.Context(), s, params.Name, params.Arguments, nil,
			generated.McpInvocationsStatusError, apierrors.McpProtocolFrameMalformed.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpProtocolFrameMalformed, valErr.Error())
		return
	}
	// Enforce the tool's own advertised input schema. The constraints a
	// tool publishes through tools/list are the ones the server applies,
	// so a tool cannot advertise a bound it does not have — see
	// argvalidate.go.
	if schemaErr := validateArgsAgainstSchema(t.inputSchema, params.Arguments); schemaErr != nil {
		h.audit(r.Context(), s, params.Name, params.Arguments, nil,
			generated.McpInvocationsStatusError, apierrors.McpToolArgumentsInvalid.Code, 0)
		writeRPCAppError(w, req.ID, apierrors.McpToolArgumentsInvalid, schemaErr.Error())
		return
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
	// Attribute the call to the task it touched. The tool bodies do not
	// know they are being audited; the resolvers in acl.go stamp the id
	// on this slot as they authorize, so every task-targeting tool lands
	// a non-NULL mcp_invocations.task_id without a per-tool change.
	runCtx = withInvocationTarget(runCtx)
	result, toolErr := t.run(runCtx, h.deps, s, args)
	dur := int32(time.Since(start).Milliseconds()) //#nosec G115 -- per-tool execution duration in ms is bounded well within int32 (~24 days)
	if toolErr != nil {
		var ae *apierrors.APIError
		spec := apierrors.McpToolExecutionFailed
		if errors.As(toolErr, &ae) {
			spec = ae.Spec
		}
		h.audit(runCtx, s, params.Name, args, nil,
			generated.McpInvocationsStatusError, spec.Code, dur)
		// Use the stable spec message rather than the raw error to
		// avoid leaking internal details (DB errors, paths, etc.).
		writeRPCAppError(w, req.ID, spec, spec.Message)
		return
	}
	resultJSON, _ := json.Marshal(result)
	// Redact secrets from the response text so MCP callers never see
	// raw API keys, tokens, or passwords in tool output. We redact here
	// FIRST so the same redacted bytes flow into both the HTTP response
	// AND the audit row — the audit helper redacts again defensively
	// (centralized invariant), but the call site MUST never hand audit
	// or the client raw secret-bearing JSON.
	_, redactedResult := redactAuditPayloads(args, resultJSON)
	h.audit(runCtx, s, params.Name, args, redactedResult,
		generated.McpInvocationsStatusOk, "", dur)
	// MCP spec: result content is a list of content parts. We wrap the
	// tool output as a single JSON text part.
	writeRPCResult(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(redactedResult)},
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

// writeRPCAppError writes a JSON-RPC error envelope at HTTP 200. Use
// this for errors that occur AFTER the request was successfully routed
// (auth ok, frame valid) — application-level failures belong inside the
// JSON-RPC envelope per JSON-RPC 2.0 convention. Examples: unknown
// method, malformed params, tool not found, scope insufficient,
// workspace mismatch resolved during tool dispatch, agent guard
// rejection, tool execution failure (including ACL denials raised by
// tool handlers).
//
// The HTTP status is intentionally left implicit (200). The spec code
// is preserved on the wire via the JSON-RPC error envelope's
// data.code field, which is the stable contract for callers that need
// to distinguish error categories programmatically.
func writeRPCAppError(w http.ResponseWriter, id json.RawMessage, spec *apierrors.Spec, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Implicit 200; do not call WriteHeader.
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

// writeRPCTransportError writes the JSON-RPC error envelope with the
// HTTP status from the spec. Use this for transport-layer rejections —
// auth failures (missing/expired/unknown bearer), malformed JSON-RPC
// frames (pre-envelope), and rate limits — that the client should
// observe at the HTTP layer per MCP Streamable HTTP guidance. The
// envelope body shape matches [writeRPCAppError]; only the HTTP status
// differs.
func writeRPCTransportError(w http.ResponseWriter, id json.RawMessage, spec *apierrors.Spec, msg string) {
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
// agentguard.Decide needs. Returns a zero Agent (not enabled) if the
// row is missing so Decide will pause the caller.
func (h *Handler) loadAgentGuardSnapshot(ctx context.Context, agentID uint32) (agentguard.Agent, error) {
	row, err := h.deps.Queries.GetAgentGuardSnapshot(ctx, agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agentguard.Agent{Enabled: false}, nil
		}
		return agentguard.Agent{}, err
	}
	var scopes []string
	if len(row.AllowedScopesJson) > 0 {
		if uerr := json.Unmarshal(row.AllowedScopesJson, &scopes); uerr != nil {
			// The agent goes unnamed here: GetAgentGuardSnapshot projects
			// only the guard columns, so the sole identifier in scope is
			// the internal id, which must not reach a log line.
			slog.WarnContext(ctx, "mcp: malformed allowed_scopes_json", slog.String("err", uerr.Error()))
		}
	}
	var costCap *int64
	if row.MonthlyCostCapCents.Valid {
		v := int64(row.MonthlyCostCapCents.Int32)
		costCap = &v
	}
	return agentguard.Agent{
		Enabled:             row.Enabled,
		Paused:              row.Paused,
		AllowedScopes:       scopes,
		MonthlyCostCapCents: costCap,
	}, nil
}

// loadAgentMonthSpendCents returns the sum of cost_estimate (in cents)
// for ai_invocations attributed to agentID since the first day of the
// current UTC month. Used by the dispatch guard.
func (h *Handler) loadAgentMonthSpendCents(ctx context.Context, agentID uint32) (int64, error) {
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	cents, err := h.deps.Queries.SumAiCostForAgentSince(ctx, generated.SumAiCostForAgentSinceParams{
		AgentID:   sql.NullInt32{Int32: int32(agentID), Valid: true}, //#nosec G115 -- agent id is agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		InvokedAt: since,
	})
	if err != nil {
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

// maxToolArgBytes is the maximum size of a single MCP tool argument
// value. 256 KB is generous for any reasonable parameter while
// protecting against accidental or malicious multi-MB payloads.
const maxToolArgBytes = 256 * 1024 // 256 KB

// validateToolArgs checks that no individual argument exceeds the size
// limit. It parses the top-level arguments object and inspects each
// value's byte length.
func validateToolArgs(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		// Not a JSON object — let the tool handler deal with it.
		return nil
	}
	for k, v := range args {
		if len(v) > maxToolArgBytes {
			return fmt.Errorf("argument %q exceeds maximum size (%d > %d bytes)", k, len(v), maxToolArgBytes)
		}
	}
	return nil
}

// hashToken returns a hex-encoded SHA-256 of the token. Used as the
// rate limiter map key so the plaintext token is never stored in memory
// beyond the request lifetime.
func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// invocationActor splits a session into the two actor columns
// mcp_invocations records.
//
// Both are recorded, not one or the other: an agent-owned token still
// names the human who minted it, and the audit trail needs to keep that
// — but the agent is who acted. A row that carries only the user id
// leaves the timeline no way to tell agent work from the person's own,
// which is the question an AI-native product is least able to leave
// unanswered. v_workspace_activity reads agent_id first for that reason.
func invocationActor(s *session) (userID, agentID sql.NullInt32) {
	if s == nil {
		return sql.NullInt32{}, sql.NullInt32{}
	}
	userID = sql.NullInt32{Int32: int32(s.userID), Valid: true} //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	if s.agentID != 0 {
		agentID = sql.NullInt32{Int32: int32(s.agentID), Valid: true} //#nosec G115 -- agent id is ai_agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	return userID, agentID
}

// auditTransportRefusal records a refusal that happens before any tool
// is named — an expired token, or a throttled request.
//
// Both are refusals the operator has the most reason to want to see and
// the least other way to find. A tool call that is denied for scope
// leaves a row; a token that has been expired for a week and is still
// being presented every second left nothing at all, and neither did the
// burst that tripped the limiter. What the timeline showed instead was
// an agent that simply stopped appearing, which reads as an idle agent
// rather than one being turned away.
//
// tool_name is empty because the refusal happened before the frame was
// dispatched — the row records that the caller was turned away, not what
// it wanted. A nil session means the token named no workspace (unknown
// token), and there is no tenant to write the row under; those refusals
// stay unrecorded by design rather than landing in someone else's
// workspace.
func (h *Handler) auditTransportRefusal(ctx context.Context, s *session, spec *apierrors.Spec) {
	if s == nil || spec == nil {
		return
	}
	h.audit(ctx, s, "", nil, nil, generated.McpInvocationsStatusDenied, spec.Code, 0)
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
	// Redact BEFORE persisting. This is the single guarantee that
	// mcp_invocations.{arguments,result}_redacted_json never contain
	// raw API keys, tokens, or passwords. Callers that already redacted
	// upstream pay only an idempotent second pass.
	argsBlob, resBlob := redactAuditPayloads(args, result)

	userID, agentID := invocationActor(s)
	var ec sql.NullString
	if errCode != "" {
		ec = sql.NullString{String: errCode, Valid: true}
	}
	// The task this call acted on, when the tool resolved one. Answering
	// "what did the agent do to this task" from the audit trail is the
	// question an AI-native product has to be able to answer, and it
	// cannot be answered from a column that is always NULL.
	var taskID sql.NullInt32
	if id := invocationTaskID(ctx); id != 0 {
		taskID = sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	_, err := h.deps.Queries.LogMcpInvocation(ctx, generated.LogMcpInvocationParams{
		PublicID:              newPublicID(),
		WorkspaceID:           s.workspaceID,
		UserID:                userID,
		AgentID:               agentID,
		TaskID:                taskID,
		ToolName:              toolName,
		ArgumentsRedactedJson: argsBlob,
		ResultRedactedJson:    resBlob,
		Status:                status,
		ErrorCode:             ec,
		DurationMs:            sql.NullInt32{Int32: durMs, Valid: true},
		InvokedAt:             time.Now().UTC(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "mcp audit log failed", slog.String("tool", toolName), slog.Any("err", err))
	}
}

// redactAuditPayloads is the single source of truth for the
// "redact before persist / before return" invariant. It accepts the
// raw arguments and result blobs as they came out of the tool handler
// (or json.Marshal of the tool's return value) and produces the
// compact, redacted JSON that is safe to:
//
//   - write to mcp_invocations.arguments_redacted_json
//   - write to mcp_invocations.result_redacted_json
//   - hand back to the MCP client as tool output text
//
// Empty inputs are normalised to "{}" so the audit row never carries
// NULL where the caller expected a JSON document. Redaction is
// performed by ai.RedactJSONFields, which both scrubs sensitive JSON
// field values (api_key, token, password, secret, authorization,
// apikey) and replaces any registered secret prefix (sk-, mcp_, ghp_,
// AKIA, ...) wherever it appears in the text.
//
// The function is idempotent: applying it twice produces the same
// bytes as applying it once. Callers that pre-redact for the HTTP
// response can therefore pass the redacted result back through audit
// without double-marking.
func redactAuditPayloads(args, result json.RawMessage) (json.RawMessage, json.RawMessage) {
	return redactOnePayload(args), redactOnePayload(result)
}

// redactOnePayload compacts then redacts a single JSON-ish blob. If
// the blob is empty, it returns the literal "{}" so the audit row is
// always a valid JSON document.
func redactOnePayload(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		// Compact failed — fall back to scrubbing the raw bytes
		// directly so we never silently drop the row.
		return json.RawMessage(ai.RedactJSONFields(string(raw)))
	}
	return json.RawMessage(ai.RedactJSONFields(buf.String()))
}

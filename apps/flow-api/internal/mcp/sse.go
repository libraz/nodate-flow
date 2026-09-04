// Package mcp SSE (Server-Sent Events) streaming support for the MCP
// Streamable HTTP transport. Connected clients receive real-time
// JSON-RPC 2.0 notifications for workspace events.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// sseConn represents a single SSE client connection. Events are
// delivered via the send channel; the handler goroutine reads from it
// and writes to the HTTP response.
type sseConn struct {
	workspaceID uint32
	send        chan sseEvent
	done        chan struct{}
}

// sseEvent is the payload written to an SSE connection.
type sseEvent struct {
	// EventType is the SSE event field (e.g. "workspace.event").
	EventType string
	// Data is the JSON-encoded data field.
	Data string
}

// sseHub tracks active SSE connections grouped by workspace. It is
// safe for concurrent use.
type sseHub struct {
	mu    sync.RWMutex
	conns map[uint32][]*sseConn // workspaceID -> connections
}

func newSSEHub() *sseHub {
	return &sseHub{conns: make(map[uint32][]*sseConn)}
}

// add registers a connection for the given workspace and returns it.
func (hub *sseHub) add(workspaceID uint32) *sseConn {
	c := &sseConn{
		workspaceID: workspaceID,
		send:        make(chan sseEvent, 64),
		done:        make(chan struct{}),
	}
	hub.mu.Lock()
	hub.conns[workspaceID] = append(hub.conns[workspaceID], c)
	hub.mu.Unlock()
	return c
}

// remove unregisters a connection.
func (hub *sseHub) remove(c *sseConn) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	conns := hub.conns[c.workspaceID]
	for i, cc := range conns {
		if cc == c {
			hub.conns[c.workspaceID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(hub.conns[c.workspaceID]) == 0 {
		delete(hub.conns, c.workspaceID)
	}
}

// broadcast sends an event to all connections for the given workspace.
// Non-blocking: if a client's buffer is full the event is dropped for
// that client.
func (hub *sseHub) broadcast(workspaceID uint32, evt sseEvent) {
	hub.mu.RLock()
	conns := hub.conns[workspaceID]
	hub.mu.RUnlock()
	for _, c := range conns {
		select {
		case c.send <- evt:
		default:
			// Client buffer full; drop the event for this client.
		}
	}
}

// activeCount returns the number of active SSE connections for a
// workspace. Useful for diagnostics.
func (hub *sseHub) activeCount(workspaceID uint32) int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.conns[workspaceID])
}

// heartbeatInterval is the period between SSE heartbeat comments, and
// sseRevalidateInterval the period between re-checks of the credential
// the stream was opened with.
//
// They are variables so the package's own tests can shorten them; no
// production code assigns to either.
var (
	heartbeatInterval     = 30 * time.Second
	sseRevalidateInterval = 15 * time.Second
)

// authorizeStream is the gate a caller must pass to hold an event
// stream, applied when the stream opens and again on every
// revalidation tick.
//
// The stream carries every event in a workspace, which makes it a
// read of the whole workspace and nothing less. It was open to any
// valid token: a token holding no scope at all received the same feed
// as one holding read:workspace, and — because removing somebody from a
// workspace does not touch their mcp_tokens rows — so did a token
// belonging to a person who had been removed from the workspace
// entirely. Both are checked here now, on the same terms the POST path
// applies per call.
//
// Membership is asked for without a role floor. A floor belongs to a
// tool and is bound by the registry when one is dispatched; a stream
// dispatches nothing, so its session carries the zero floor and there is
// no declared minimum to hold it to. Reading the workspace's events is
// what any member may do, which is the rule the two checks below state in
// full: hold read:workspace, and still be in the workspace.
func (h *Handler) authorizeStream(ctx context.Context, sess *session) error {
	if !sess.hasScope(ScopeReadWorkspace) {
		return apierrors.New(apierrors.McpScopeInsufficient)
	}
	if _, err := requireWorkspaceMembership(ctx, h.deps, sess); err != nil {
		return err
	}
	return nil
}

// revalidateStream re-answers "may this connection still be here?"
// against the database rather than against the decision made when it
// opened. Revoking a token, letting it expire, narrowing its scopes, or
// removing its owner from the workspace all take effect on the next
// tick; before this existed, the UI promised revocation stopped access
// "immediately" while an already-open stream kept delivering every
// workspace event until the client chose to disconnect.
//
// A database error that is not "no such token" leaves the stream up.
// That is deliberate: a transient outage must not disconnect every
// connected client at once, and the failure it trades away — a
// revocation not enforced while the database is unreachable — lasts as
// long as the outage rather than as long as the connection.
func (h *Handler) revalidateStream(ctx context.Context, tok string, sess *session) error {
	if h.deps.Queries == nil {
		return nil
	}
	row, err := h.deps.Queries.FindUserForMcpToken(ctx, auth.HashOpaque(tok))
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			// The row is gone, disabled, or carries a revoked_at.
			return apierrors.New(apierrors.McpTokenRevoked)
		}
		return nil
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return apierrors.New(apierrors.McpTokenExpired)
	}
	if row.WorkspaceID != sess.workspaceID {
		return apierrors.New(apierrors.McpTokenWorkspaceMismatch)
	}
	current := &session{
		userID:      row.UserID,
		workspaceID: row.WorkspaceID,
		scopes:      parseScopes(row.ScopesJson),
	}
	if aerr := h.authorizeStream(ctx, current); aerr != nil {
		var ae *apierrors.APIError
		if stderrors.As(aerr, &ae) {
			return aerr
		}
		// Not a decision, an infrastructure failure — same trade as above.
		return nil
	}
	return nil
}

// streamClosedEvent is the last frame a closed stream receives. The
// headers went out when the stream opened, so the HTTP status can no
// longer say anything; a client that is told the code can distinguish a
// revoked credential from a dropped connection and stop reconnecting.
func streamClosedEvent(err error) sseEvent {
	code := apierrors.InternalUnexpected.Code
	var ae *apierrors.APIError
	if stderrors.As(err, &ae) && ae.Spec != nil {
		code = ae.Spec.Code
	}
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/stream_closed",
		"params":  map[string]any{"code": code},
	})
	return sseEvent{EventType: "stream.closed", Data: string(payload)}
}

// serveSSE handles GET requests for the SSE stream. It authenticates
// the caller, sets SSE headers, and pumps events until the client
// disconnects.
func (h *Handler) serveSSE(w http.ResponseWriter, r *http.Request) {
	// Validate Accept header.
	accept := r.Header.Get("Accept")
	if accept != "text/event-stream" {
		writeRPCTransportError(w, nil, apierrors.McpProtocolFrameMalformed,
			"GET requires Accept: text/event-stream")
		return
	}

	// Authenticate via Authorization: Bearer mcp_...
	tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
	if !ok || !strings.HasPrefix(tok, auth.PrefixMCP) {
		writeRPCTransportError(w, nil, apierrors.McpTokenUnknown, "missing mcp bearer")
		return
	}
	sess, err := h.authenticate(r.Context(), tok)
	if err != nil {
		var ae *apierrors.APIError
		if stderrors.As(err, &ae) {
			// Same refusal as the POST path, and it has to be recorded on
			// both: a client shut out of tool calls will keep reopening
			// the stream, and that is the traffic an operator sees first.
			h.auditTransportRefusal(r.Context(), sess, ae.Spec)
			writeRPCTransportError(w, nil, ae.Spec, ae.Spec.Message)
			return
		}
		writeRPCTransportError(w, nil, apierrors.InternalUnexpected, "auth failed")
		return
	}

	// Scope and membership are checked before the stream is registered,
	// not only when a tool is called: the stream itself is a read of the
	// workspace.
	if aerr := h.authorizeStream(r.Context(), sess); aerr != nil {
		var ae *apierrors.APIError
		if stderrors.As(aerr, &ae) {
			writeRPCTransportError(w, nil, ae.Spec, ae.Spec.Message)
			return
		}
		writeRPCTransportError(w, nil, apierrors.InternalUnexpected, "stream authorization failed")
		return
	}

	// Per-token rate limiting (counts the SSE connection initiation).
	// Hash the token so the plaintext is never stored as a map key in the
	// rate limiter, AND so the GET (SSE) and POST paths share one budget
	// under the same hashed key — preventing a client from doubling its
	// rate allowance by splitting requests across the two methods.
	if allowed, retryAfter, first := h.rl.allow(hashToken(tok)); !allowed {
		if first {
			h.auditTransportRefusal(r.Context(), sess, apierrors.RateLimitExceeded)
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		writeRPCTransportError(w, nil, apierrors.RateLimitExceeded, "rate limit exceeded")
		return
	}

	// Verify streaming support.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCTransportError(w, nil, apierrors.InternalUnexpected, "streaming not supported")
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Register this connection.
	conn := h.sse.add(sess.workspaceID)
	defer func() {
		h.sse.remove(conn)
		close(conn.done)
	}()

	ctx := r.Context()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(sseRevalidateInterval)
	defer revalidate.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-conn.send:
			if err := writeSSEEvent(w, flusher, evt); err != nil {
				return
			}
		case <-revalidate.C:
			if rerr := h.revalidateStream(ctx, tok, sess); rerr != nil {
				_ = writeSSEEvent(w, flusher, streamClosedEvent(rerr))
				return
			}
		case <-heartbeat.C:
			if err := writeSSEComment(w, flusher, "heartbeat"); err != nil {
				return
			}
		}
	}
}

// writeSSEEvent writes a single SSE event frame and flushes.
func writeSSEEvent(w http.ResponseWriter, f http.Flusher, evt sseEvent) error {
	if evt.EventType != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", evt.EventType); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", evt.Data); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// writeSSEComment writes a comment line (used for heartbeats) and
// flushes.
func writeSSEComment(w http.ResponseWriter, f http.Flusher, text string) error {
	if _, err := fmt.Fprintf(w, ":%s\n\n", text); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// buildEventNotification constructs a JSON-RPC 2.0 notification
// payload for a workspace event. Notifications have no id per the
// JSON-RPC 2.0 spec. The seq field counts the events of one workspace:
// it is dense within the stream a client subscribes to and says nothing
// about any other workspace, so a gap means the client missed one of
// its own events rather than that another tenant was busy.
func buildEventNotification(eventType string, seq int64) string {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/event",
		"params": map[string]any{
			"type": eventType,
			"seq":  seq,
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// onWorkspaceEvent is the eventbus.NotifyHook callback that broadcasts
// events to SSE-connected MCP clients. It must be non-blocking. The
// eventInternalID parameter is accepted for signature compatibility
// but unused — MCP clients receive only the type and seq.
func (h *Handler) onWorkspaceEvent(ctx context.Context, workspaceID uint32, eventType string, _ uint64) {
	seq := eventbus.SeqFromContext(ctx)
	h.sse.broadcast(workspaceID, sseEvent{
		EventType: "workspace.event",
		Data:      buildEventNotification(eventType, seq),
	})
}

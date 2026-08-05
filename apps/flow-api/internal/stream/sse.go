package stream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// writeJSONError writes a structured JSON error response for the SSE
// handler. It matches the standard apierrors envelope shape so clients
// receive a consistent error format even on upgrade-time failures.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  status,
		"code":    code,
		"message": message,
	})
}

// heartbeatInterval is the idle cadence at which the SSE writer
// emits a comment line (": ping") so intermediaries don't close the
// connection. ADR 0005 fixes this at 20 seconds.
const heartbeatInterval = 20 * time.Second

// initialRetryMillis is the SSE `retry:` hint the server writes on
// open. Compliant readers use it as the reconnect floor.
const initialRetryMillis = 5000

// RememberWorkspaceFunc is the callback the SSE handler uses to
// teach the eventbus tap the internal→public id mapping for the
// workspace the caller just subscribed to. It is the only way the
// tap learns public ids without hitting the database on the append
// hot path.
type RememberWorkspaceFunc func(internalID uint32, publicID string)

// SSEHandler returns an http.Handler that upgrades the request to
// an SSE stream scoped to the workspace resolved by
// [middleware.WorkspaceFromContext]. It expects the upstream router
// to have already enforced authentication and workspace membership.
//
// remember is called exactly once per subscription with the
// workspace's internal and public ids so the eventbus tap can
// resolve future appends. Pass a no-op when streaming is disabled.
//
// The handler runs until the client disconnects or the request
// context is cancelled. Each published [Event] is written as a
// single SSE frame; idle periods are punctuated by ": ping" comment
// lines at [heartbeatInterval].
func SSEHandler(notifier Notifier, remember RememberWorkspaceFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, ok := middleware.WorkspaceFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusForbidden, "WS.WORKSPACE.NOT_FOUND", "workspace context missing")
			return
		}
		if remember != nil {
			remember(ws.ID, ws.PublicID.String())
		}
		// http.NewResponseController transparently unwraps any
		// middleware ResponseWriter wrappers (e.g. the request-logger's
		// statusRecorder) to find an underlying Flusher. A bare
		// `w.(http.Flusher)` type assertion fails the moment any
		// middleware in the chain embeds http.ResponseWriter without
		// also forwarding Flush, which is what was happening here.
		rc := http.NewResponseController(w)
		flush := func() { _ = rc.Flush() }

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// Opt out of any reverse-proxy buffering that would turn SSE
		// into a batch response. Nginx and some Caddy configs honour
		// this header directly; others need proxy_buffering off at
		// the proxy config level (see ADR 0005 §Consequences).
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// Register the subscription BEFORE emitting the resync frame.
		// The client treats "received resync" as "I am subscribed,
		// safe to act", so the subscriber must already be in the
		// notifier registry when the frame is flushed. Subscribing
		// afterwards would drop any event published in the window
		// between the flush and Subscribe. The per-subscriber inbox is
		// buffered, so any event published between here and the read
		// loop below is queued, not lost.
		ctx := r.Context()
		ch := notifier.Subscribe(ctx, ws.PublicID.String())

		// Initial retry hint + a synthetic resync marker so the
		// client can invalidate its workspace queries once on
		// connect and stay in sync regardless of what happened
		// before the subscription started.
		_, _ = fmt.Fprintf(w, "retry: %d\n\n", initialRetryMillis)
		writeEvent(w, Event{
			Kind:        KindResync,
			WorkspaceID: ws.PublicID.String(),
			At:          time.Now().Unix(),
		})
		flush()

		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				writeEvent(w, evt)
				flush()
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				flush()
			}
		}
	}
}

// writeEvent serialises a single SSE frame. Format:
//
//	event: <kind>
//	data: <json>
//	\n
//
// The two trailing newlines terminate the frame.
func writeEvent(w http.ResponseWriter, evt Event) {
	payload, err := json.Marshal(evt)
	if err != nil {
		// Should never happen for the closed Event shape.
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Kind, payload)
}

package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	gh "github.com/nodate-flow/nodate-flow/apps/api/internal/integrations/github"
)

// taskMarker is the inline body marker convention that lets a GitHub
// PR / issue body announce which task it relates to. Example:
//
//	tnk:01931c2c-7e3a-7c66-9b85-3a0c9b3a8eaa
const taskMarker = "tnk:"

// extractTaskMarker scans body for the first occurrence of "tnk:<uuid>"
// and returns the parsed UUID if found. It deliberately avoids regular
// expressions; the marker is fixed-shape (4 chars + 36 chars).
func extractTaskMarker(body []byte) (string, bool) {
	s := string(body)
	idx := strings.Index(s, taskMarker)
	if idx < 0 {
		return "", false
	}
	rest := s[idx+len(taskMarker):]
	if len(rest) < 36 {
		return "", false
	}
	candidate := rest[:36]
	if _, err := types.Parse(candidate); err != nil {
		return "", false
	}
	return candidate, true
}

// HandleGithubWebhook is a chi-level handler for POST /webhooks/github.
// It verifies the X-Hub-Signature-256 header and inserts a signals row.
//
// Phase 1 routes every inbound delivery to deps.DefaultWorkspaceID; a
// real per-repo workspace mapping table is deferred to a later phase.
func HandleGithubWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, apierrors.IntegrationGhWebhookPayloadUnparseable)
			return
		}
		sig := r.Header.Get(gh.SignatureHeader)
		if !gh.VerifySignature(body, sig, deps.GhWebhookSecret) {
			writeError(w, apierrors.IntegrationGhWebhookInvalidSignature)
			return
		}
		event := r.Header.Get(gh.EventHeader)
		if event == "" {
			writeError(w, apierrors.IntegrationGhWebhookEventUnsupported)
			return
		}

		// Resolve workspace from the configured default. Phase 1 only.
		if deps.DefaultWorkspaceID == "" {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		wsPub, err := types.Parse(deps.DefaultWorkspaceID)
		if err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		ctx := r.Context()
		const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
		var wsID uint32
		if err := deps.DB.QueryRowContext(ctx, wsLookup, wsPub).Scan(&wsID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, apierrors.WsWorkspaceNotFound)
				return
			}
			writeError(w, apierrors.InternalUnexpected)
			return
		}

		// Best-effort task linkage from a body marker.
		var taskFK sql.NullInt32
		var taskInternal int64
		var taskLinked bool
		if marker, ok := extractTaskMarker(body); ok {
			id, found, terr := resolveTaskInWorkspace(ctx, deps.DB, wsID, marker)
			if terr == nil && found {
				taskFK = sql.NullInt32{Int32: int32(id), Valid: true}
				taskInternal = id
				taskLinked = true
			}
		}

		// Validate body is JSON; otherwise store as opaque object so the
		// signals row stays well-formed.
		payload := body
		if !json.Valid(body) {
			payload = json.RawMessage(`{}`)
		}

		pub := types.New()
		now := time.Now().UTC()
		ext := sql.NullString{String: r.Header.Get("X-GitHub-Delivery"), Valid: r.Header.Get("X-GitHub-Delivery") != ""}
		if _, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			TaskID:      taskFK,
			Source:      generated.SignalsSourceGithub,
			Kind:        event,
			ExternalID:  ext,
			PayloadJson: payload,
			ReceivedAt:  now,
		}); err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}

		if taskLinked {
			_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
				Type:        "signal.attached",
				WorkspaceID: wsID,
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"signalId": pub.String(),
					"source":   "github",
					"kind":     event,
				},
			})
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":     pub.String(),
			"linked": taskLinked,
		})
	}
}

// writeError writes the canonical error envelope from an error spec at
// the chi layer (we cannot use Huma's pipeline here because the webhook
// route is not registered through Huma).
func writeError(w http.ResponseWriter, spec *apierrors.Spec) {
	writeJSON(w, spec.Status, map[string]any{
		"code":    spec.Code,
		"message": spec.Message,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}


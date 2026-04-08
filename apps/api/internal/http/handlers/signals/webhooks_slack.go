package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	sl "github.com/nodate-flow/nodate-flow/apps/api/internal/integrations/slack"
)

// HandleSlackWebhook is a chi-level handler for POST /webhooks/slack.
// It verifies the v0 Slack signing-secret scheme and inserts a signals row.
func HandleSlackWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, apierrors.IntegrationSlackWebhookPayloadUnparseable)
			return
		}
		ts := r.Header.Get(sl.TimestampHeader)
		sig := r.Header.Get(sl.SignatureHeader)
		if !sl.VerifySignature(body, sig, ts, deps.SlackSigningSecret, time.Now()) {
			writeError(w, apierrors.IntegrationSlackWebhookSigningFailed)
			return
		}

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

		// Pull the inner event type from { "event": { "type": "..." } }
		// if present; otherwise fall back to the top-level type.
		kind := "unknown"
		var envelope struct {
			Type  string `json:"type"`
			Event struct {
				Type string `json:"type"`
			} `json:"event"`
		}
		if json.Valid(body) {
			_ = json.Unmarshal(body, &envelope)
			kind = sl.NormalizeEventKind(envelope.Type, envelope.Event.Type)
		}
		payload := body
		if !json.Valid(body) {
			payload = json.RawMessage(`{}`)
		}

		pub := types.New()
		now := time.Now().UTC()
		if _, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			Source:      generated.SignalsSourceSlack,
			Kind:        kind,
			PayloadJson: payload,
			ReceivedAt:  now,
		}); err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": pub.String()})
	}
}

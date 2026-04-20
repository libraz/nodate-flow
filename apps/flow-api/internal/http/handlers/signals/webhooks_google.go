package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	goog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/integrations/google"
)

// HandleGoogleWebhook is a chi-level handler for POST /webhooks/google.
// Google Drive push notifications do not sign payloads; instead each
// channel is registered with a unique X-Goog-Channel-Token that we
// verify with a constant-time compare against [Deps.GoogleChannelToken].
func HandleGoogleWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, apierrors.IntegrationGhWebhookPayloadUnparseable)
			return
		}
		token := r.Header.Get(goog.HeaderChannelToken)
		if !goog.VerifyChannelToken(token, deps.GoogleChannelToken) {
			writeError(w, apierrors.IntegrationGoogleWebhookInvalidToken)
			return
		}
		kind := goog.NormalizeEventKind(r.Header.Get(goog.HeaderResourceState))

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

		payload := body
		if len(payload) == 0 || !json.Valid(payload) {
			payload = json.RawMessage(`{}`)
		}

		ext := sql.NullString{String: r.Header.Get(goog.HeaderChannelID), Valid: r.Header.Get(goog.HeaderChannelID) != ""}
		pub := types.New()
		if _, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			Source:      generated.SignalsSourceGoogle,
			Kind:        kind,
			ExternalID:  ext,
			PayloadJson: payload,
			ReceivedAt:  time.Now().UTC(),
		}); err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": pub.String()})
	}
}

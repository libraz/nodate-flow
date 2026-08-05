package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	goog "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/google"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// HandleGoogleWebhook is a chi-level handler for POST /webhooks/google.
// Google Drive push notifications do not sign payloads; instead each
// channel is registered with a unique X-Goog-Channel-Token that we
// verify with a constant-time compare against [Deps.GoogleChannelToken].
func HandleGoogleWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			slog.ErrorContext(r.Context(), "webhook: google body read failed",
				slog.Any("error", err),
				slog.String("source", "google"),
			)
			writeError(w, apierrors.IntegrationGhWebhookPayloadUnparseable)
			return
		}
		token := r.Header.Get(goog.HeaderChannelToken)
		if !goog.VerifyChannelToken(token, deps.GoogleChannelToken) {
			slog.ErrorContext(r.Context(), "webhook: google channel token verification failed",
				slog.String("source", "google"),
				slog.Bool("token_present", token != ""),
			)
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
			slog.ErrorContext(r.Context(), "webhook: google default workspace id parse failed",
				slog.Any("error", err),
				slog.String("source", "google"),
			)
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
			slog.ErrorContext(ctx, "webhook: google workspace lookup failed",
				slog.Any("error", err),
				slog.String("source", "google"),
			)
			writeError(w, apierrors.InternalUnexpected)
			return
		}

		payload := body
		if len(payload) == 0 || !json.Valid(payload) {
			payload = json.RawMessage(`{}`)
		}

		// Google Drive push notifications today are not yet resolved to a
		// specific calendar_event row at the webhook layer — the channel
		// metadata in the headers identifies the watched resource, not the
		// individual event row. resolveSubjectType returns "workspace" for
		// the free-form Drive kinds NormalizeEventKind emits, which keeps
		// signals.subject_type NOT NULL satisfied without claiming a more
		// specific subject than we actually resolved.
		//
		// TODO(signal_kinds): once Phase 4 promotes calendar push events to
		// the signal_kinds/calendar.yaml registry and a per-channel
		// calendar_event resolver is in place, switch to
		// SignalsSubjectTypeCalendarEvent with the resolved internal id.
		ext := sql.NullString{String: r.Header.Get(goog.HeaderChannelID), Valid: r.Header.Get(goog.HeaderChannelID) != ""}
		subjectType := resolveSubjectType(kind, "")
		subjectID := subjectIDFor(subjectType, 0)
		pub := types.New()
		signalInternalID, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			Source:      generated.SignalsSourceGoogle,
			Kind:        kind,
			ExternalID:  ext,
			PayloadJson: payload,
			ReceivedAt:  time.Now().UTC(),
			SubjectType: subjectType,
			SubjectID:   subjectID,
		})
		if err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}

		// Best-effort signal_judge dispatch (ADR 0008 D3).
		if deps.JudgeEnqueuer != nil {
			if jerr := deps.JudgeEnqueuer.EnqueueForSignal(ctx, signalInternalID, wsID, signalkinds.Kind(kind)); jerr != nil {
				slog.WarnContext(ctx, "signaljudge enqueue failed",
					slog.Any("err", jerr),
					slog.String("handler", "signals.HandleGoogleWebhook"),
					slog.String("signal_public_id", pub.String()),
					slog.String("kind", kind),
				)
			}
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": pub.String()})
	}
}

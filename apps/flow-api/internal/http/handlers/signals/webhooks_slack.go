package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	sl "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
)

// HandleSlackWebhook is a chi-level handler for POST /webhooks/slack.
// It verifies the v0 Slack signing-secret scheme and inserts a signals row.
func HandleSlackWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			slog.ErrorContext(r.Context(), "webhook: slack body read failed",
				slog.Any("error", err),
				slog.String("source", "slack"),
			)
			writeError(w, apierrors.IntegrationSlackWebhookPayloadUnparseable)
			return
		}
		ts := r.Header.Get(sl.TimestampHeader)
		sig := r.Header.Get(sl.SignatureHeader)
		if verr := sl.VerifySignature(body, sig, ts, deps.SlackSigningSecret, time.Now()); verr != nil {
			slog.ErrorContext(r.Context(), "webhook: slack signature verification failed",
				slog.Any("error", verr),
				slog.String("source", "slack"),
			)
			writeError(w, slackVerifyErrorSpec(verr))
			return
		}

		if deps.DefaultWorkspaceID == "" {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		wsPub, err := types.Parse(deps.DefaultWorkspaceID)
		if err != nil {
			slog.ErrorContext(r.Context(), "webhook: slack default workspace id parse failed",
				slog.Any("error", err),
				slog.String("source", "slack"),
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
			slog.ErrorContext(ctx, "webhook: slack workspace lookup failed",
				slog.Any("error", err),
				slog.String("source", "slack"),
			)
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
			if uerr := json.Unmarshal(body, &envelope); uerr != nil {
				slog.WarnContext(r.Context(), "webhook: slack envelope unmarshal", "error", uerr)
			}
			kind = sl.NormalizeEventKind(envelope.Type, envelope.Event.Type)
		}
		// When NormalizeEventKind produces "slack.presence" the row already
		// matches the signal_kinds/presence.yaml registry entry verbatim,
		// so resolveSubjectType below dispatches to the typed default
		// (subject_type=user). Slack message and reaction events remain as
		// the free-form strings NormalizeEventKind emits until they earn
		// signal_kinds/slack.yaml entries (TODO: registry expansion when
		// the judge prompt needs them).
		payload := body
		if !json.Valid(body) {
			payload = json.RawMessage(`{}`)
		}

		// Slack does not (yet) resolve the per-user subject from this
		// webhook entrypoint — the mention/user normalisation is a follow-up
		// when slack OAuth lands per ADR 0008 D6. Until then the row is
		// workspace-scoped, which keeps signals.subject_type NOT NULL
		// satisfied without claiming a user we have not actually resolved.
		subjectType := resolveSubjectType(kind, "")
		subjectID := subjectIDFor(subjectType, 0)

		pub := types.New()
		now := time.Now().UTC()
		signalInternalID, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			Source:      generated.SignalsSourceSlack,
			Kind:        kind,
			PayloadJson: payload,
			ReceivedAt:  now,
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
					slog.String("handler", "signals.HandleSlackWebhook"),
					slog.String("signal_public_id", pub.String()),
					slog.String("kind", kind),
				)
			}
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": pub.String()})
	}
}

// slackVerifyErrorSpec maps a sentinel error from sl.VerifySignature to
// the matching public API error spec. Unknown errors fall back to the
// generic mismatch code so we never leak unclassified internals.
func slackVerifyErrorSpec(verr error) *apierrors.Spec {
	switch {
	case errors.Is(verr, sl.ErrSignatureMissing):
		return apierrors.IntegrationSlackWebhookSignatureMissing
	case errors.Is(verr, sl.ErrSignatureMalformed):
		return apierrors.IntegrationSlackWebhookSignatureMalformed
	case errors.Is(verr, sl.ErrTimestampExpired):
		return apierrors.IntegrationSlackWebhookTimestampExpired
	default:
		return apierrors.IntegrationSlackWebhookSignatureMismatch
	}
}

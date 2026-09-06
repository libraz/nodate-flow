package signals

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// slackURLVerification is the envelope `type` Slack POSTs once when a
// Request URL is saved in an app's Event Subscriptions settings.
const slackURLVerification = "url_verification"

// HandleSlackWebhook is a chi-level handler for POST /webhooks/slack.
// It verifies the v0 Slack signing-secret scheme, answers the Events API
// URL verification handshake, and inserts a signals row.
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

		// The envelope is parsed before anything is resolved or written
		// because the URL verification handshake below has to be answered
		// on an instance that has no workspace configured yet.
		var envelope struct {
			Type      string `json:"type"`
			Challenge string `json:"challenge"`
			EventID   string `json:"event_id"`
			TeamID    string `json:"team_id"`
			Event     struct {
				Type string `json:"type"`
			} `json:"event"`
		}
		bodyIsJSON := json.Valid(body)
		if bodyIsJSON {
			if uerr := json.Unmarshal(body, &envelope); uerr != nil {
				slog.WarnContext(r.Context(), "webhook: slack envelope unmarshal", "error", uerr)
			}
		}

		// Events API URL verification: saving a Request URL makes Slack
		// POST {"type":"url_verification","challenge":"..."} and accept the
		// URL only if the response echoes `challenge` back with 200.
		// Without this the app's event subscription can never be enabled,
		// so no other branch below is reachable in a real deployment.
		//
		// The handshake is signed like every other delivery, so it is
		// handled after VerifySignature — an unsigned prober must not be
		// able to discover that this path exists — but before workspace
		// resolution and the insert, since no event has occurred yet and
		// there is nothing to persist.
		if envelope.Type == slackURLVerification {
			if envelope.Challenge == "" {
				writeError(w, apierrors.IntegrationSlackWebhookPayloadUnparseable)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"challenge": envelope.Challenge})
			return
		}

		// `team_id` is the Slack workspace the event happened in and is
		// present on every event callback. It is the routing key: one
		// Slack workspace maps to one flow workspace, and the envelope
		// it comes from has already passed signature verification.
		ctx := r.Context()
		wsID, spec := resolveWebhookWorkspace(ctx, deps, webhookSender{
			Provider: generated.IntegrationSourceMappingsProviderSlack,
			Key:      envelope.TeamID,
			Source:   "slack",
		})
		if spec != nil {
			writeError(w, spec)
			return
		}

		// Pull the inner event type from { "event": { "type": "..." } }
		// if present; otherwise fall back to the top-level type.
		kind := "unknown"
		if bodyIsJSON {
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
		if !bodyIsJSON {
			payload = json.RawMessage(`{}`)
		}

		// `event_id` is the top-level identifier Slack assigns to an event
		// callback; it is carried unchanged on every retry of the same
		// event, which is exactly the dedupe semantics
		// (workspace_id, source, external_id) needs. Envelope types that
		// carry no event_id (interactivity payloads, future shapes) stay
		// NULL and are simply not deduped.
		ext := dedupeKey(envelope.EventID)

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
			ExternalID:  ext,
			PayloadJson: payload,
			ReceivedAt:  now,
			SubjectType: subjectType,
			SubjectID:   subjectID,
		})
		if err != nil {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		if respondIfDuplicate(ctx, w, deps, wsID, generated.SignalsSourceSlack, ext, signalInternalID, "signals.HandleSlackWebhook") {
			return
		}

		// A delivery from outside still changes the workspace, so it is
		// recorded in both logs like any other ingestion. There is no
		// authenticated user behind it: Actor.UserID zero is what says so,
		// and both rows carry no actor rather than a fabricated one.
		//
		// It sits after the duplicate check because a retry of the same
		// event_id collides on the dedupe key and writes no signals row, so
		// there is no ingestion for it to describe.
		deps.Mutations.Record(ctx, mutationlog.Actor{WorkspaceID: wsID}, mutationlog.Mutation{
			EventType:    eventbus.SignalAttached,
			AuditAction:  "signal.create",
			ResourceType: "signal",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"signalId": pub.String(),
				"source":   "slack",
				"kind":     kind,
			},
			CallSite: "signals.HandleSlackWebhook",
		})

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

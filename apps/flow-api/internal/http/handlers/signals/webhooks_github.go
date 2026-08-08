package signals

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	gh "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/github"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
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
// It verifies the X-Hub-Signature-256 header, routes the delivery to the
// workspace that owns the sending repository, and inserts a signals row.
func HandleGithubWebhook(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			slog.ErrorContext(r.Context(), "webhook: github body read failed",
				slog.Any("error", err),
				slog.String("source", "github"),
			)
			writeError(w, apierrors.IntegrationGhWebhookPayloadUnparseable)
			return
		}
		sig := r.Header.Get(gh.SignatureHeader)
		if !gh.VerifySignature(body, sig, deps.GhWebhookSecret) {
			slog.ErrorContext(r.Context(), "webhook: github signature verification failed",
				slog.String("source", "github"),
				slog.Bool("signature_present", sig != ""),
			)
			writeError(w, apierrors.IntegrationGhWebhookInvalidSignature)
			return
		}
		event := r.Header.Get(gh.EventHeader)
		if event == "" {
			writeError(w, apierrors.IntegrationGhWebhookEventUnsupported)
			return
		}
		// Normalize event + action into a stable kind so the
		// constraint engine and timeline filters can rely on a single
		// shape across every github source. The same pass reads
		// repository.id, which is the routing key: it is the stable
		// numeric identity of the sending repository (a rename changes
		// full_name but not the id), and the body it comes from has
		// already passed HMAC verification above.
		var routeEnv struct {
			Action     string `json:"action"`
			Repository struct {
				ID int64 `json:"id"`
			} `json:"repository"`
		}
		if json.Valid(body) {
			if uerr := json.Unmarshal(body, &routeEnv); uerr != nil {
				slog.WarnContext(r.Context(), "webhook: github action unmarshal", "error", uerr)
			}
		}
		event = gh.NormalizeEventKind(event, routeEnv.Action)

		ctx := r.Context()
		wsID, spec := resolveWebhookWorkspace(ctx, deps, webhookSender{
			Provider: generated.IntegrationSourceMappingsProviderGithub,
			Key:      githubSenderKey(routeEnv.Repository.ID),
			Source:   "github",
		})
		if spec != nil {
			writeError(w, spec)
			return
		}

		// Best-effort task linkage from a body marker. The lookup is
		// scoped to wsID — the workspace the repository is mapped to —
		// so a `tnk:<uuid>` written into an issue body can only ever
		// name a task in that repository's own workspace. A marker
		// pointing at another tenant's task simply does not resolve and
		// the signal stays workspace-scoped.
		var taskFK sql.NullInt32
		var taskInternal int64
		var taskLinked bool
		if marker, ok := extractTaskMarker(body); ok {
			id, found, terr := resolveTaskInWorkspace(ctx, deps.DB, wsID, marker)
			if terr == nil && found {
				taskFK = sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
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
		// X-GitHub-Delivery is the delivery's own id and is repeated
		// verbatim on every redelivery of it, which is what the
		// (workspace_id, source, external_id) dedupe key needs.
		ext := dedupeKey(r.Header.Get("X-GitHub-Delivery"))
		// GitHub webhook kinds (issue / pull_request / commit events) are
		// NOT yet members of the signal_kinds/*.yaml registry, so the
		// registry lookup misses and resolveSubjectType falls back to
		// SignalsSubjectTypeWorkspace. The task linkage (extracted from the
		// `tnk:<uuid>` body marker) is recorded on the legacy `task_id`
		// column; subject_type is upgraded to `task` so the new addressing
		// shape stays consistent with the legacy fast path.
		//
		// TODO(signal_kinds): promote `github.issue.*`, `github.pr.*`, and
		// `github.commit.*` to signal_kinds/github.yaml once the judge
		// prompt schema needs them (ADR 0008 D2). Until then leaving the
		// kind values as the free-form strings NormalizeEventKind produces
		// keeps the wire shape stable.
		subjectType := resolveSubjectType(event, "")
		if taskLinked {
			subjectType = generated.SignalsSubjectTypeTask
		}
		subjectID := subjectIDFor(subjectType, taskInternal)
		signalInternalID, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			TaskID:      taskFK,
			Source:      generated.SignalsSourceGithub,
			Kind:        event,
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
		// A redelivery of the same X-GitHub-Delivery collides on the
		// dedupe key and writes nothing, so it must not mint a public id,
		// append a second signal.attached event, or re-wake the judge for
		// a signal the first delivery already filed.
		if respondIfDuplicate(ctx, w, deps, wsID, generated.SignalsSourceGithub, ext, signalInternalID, "signals.HandleGithubWebhook") {
			return
		}

		if taskLinked {
			if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
				Type:        eventbus.SignalAttached,
				WorkspaceID: wsID,
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"signalId": pub.String(),
					"source":   "github",
					"kind":     event,
				},
			}); err != nil {
				slog.ErrorContext(ctx, "eventbus.Append failed",
					slog.Any("err", err),
					slog.String("handler", "signals.HandleGithubWebhook"),
					slog.String("event_type", string(eventbus.SignalAttached)),
					slog.String("signal_id", pub.String()),
					slog.String("kind", event),
				)
			}
		}

		// Best-effort signal_judge dispatch (ADR 0008 D3). The
		// enqueuer logs and swallows errors so a flaky agent_runs
		// insert cannot fail the webhook ACK; webhook redelivery
		// retries the whole pipeline including the enqueue.
		if deps.JudgeEnqueuer != nil {
			if jerr := deps.JudgeEnqueuer.EnqueueForSignal(ctx, signalInternalID, wsID, signalkinds.Kind(event)); jerr != nil {
				slog.WarnContext(ctx, "signaljudge enqueue failed",
					slog.Any("err", jerr),
					slog.String("handler", "signals.HandleGithubWebhook"),
					slog.String("signal_public_id", pub.String()),
					slog.String("kind", event),
				)
			}
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":     pub.String(),
			"linked": taskLinked,
		})
	}
}

// webhookErr writes the canonical apierror envelope from an error spec
// at the chi layer (the webhook routes are not registered through Huma,
// so the framework's ProblemDetails pipeline cannot run). The wire
// shape mirrors handlerutil.HTTPErr's RFC 9457 envelope (`type` /
// `title` / `status` / `detail` / `description` / `userAction`) so SDK
// clients branch on the same `type` field they receive from Huma
// endpoints.
func webhookErr(w http.ResponseWriter, spec *apierrors.Spec) {
	handlerutil.WriteSpecError(w, spec)
}

// writeError is preserved as a thin alias so existing callers in this
// package keep compiling; new code should call webhookErr directly.
func writeError(w http.ResponseWriter, spec *apierrors.Spec) {
	webhookErr(w, spec)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

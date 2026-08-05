package signals

import (
	"database/sql"
	"encoding/json"
	"errors"
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
// It verifies the X-Hub-Signature-256 header and inserts a signals row.
//
// Currently routes every inbound delivery to deps.DefaultWorkspaceID;
// a real per-repo workspace mapping table is deferred.
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
		// shape across every github source.
		var actionEnv struct {
			Action string `json:"action"`
		}
		if json.Valid(body) {
			if uerr := json.Unmarshal(body, &actionEnv); uerr != nil {
				slog.WarnContext(r.Context(), "webhook: github action unmarshal", "error", uerr)
			}
		}
		event = gh.NormalizeEventKind(event, actionEnv.Action)

		// Resolve workspace from the configured default.
		if deps.DefaultWorkspaceID == "" {
			writeError(w, apierrors.InternalUnexpected)
			return
		}
		wsPub, err := types.Parse(deps.DefaultWorkspaceID)
		if err != nil {
			slog.ErrorContext(r.Context(), "webhook: github default workspace id parse failed",
				slog.Any("error", err),
				slog.String("source", "github"),
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
			slog.ErrorContext(ctx, "webhook: github workspace lookup failed",
				slog.Any("error", err),
				slog.String("source", "github"),
			)
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
		ext := sql.NullString{String: r.Header.Get("X-GitHub-Delivery"), Valid: r.Header.Get("X-GitHub-Delivery") != ""}
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
					slog.Int64("workspace_id", int64(wsID)),
					slog.Int64("task_id", taskInternal),
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

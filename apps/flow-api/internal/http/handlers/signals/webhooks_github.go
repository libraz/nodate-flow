package signals

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	gh "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/github"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

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

		// Validate body is JSON; otherwise store as opaque object so the
		// signals row stays well-formed.
		payload := body
		if !json.Valid(body) {
			payload = json.RawMessage(`{}`)
		}

		// GitHub kinds are not in the signal_kinds registry yet, so the
		// subject falls back to the workspace unless a body marker binds a task.
		subjectType := resolveSubjectType(event, "")
		params := generated.InsertSignalParams{
			WorkspaceID: wsID,
			Source:      generated.SignalsSourceGithub,
			Kind:        event,
			// X-GitHub-Delivery repeats verbatim on every redelivery.
			ExternalID:  dedupeKey(r.Header.Get("X-GitHub-Delivery")),
			PayloadJson: payload,
			ReceivedAt:  time.Now().UTC(),
			SubjectType: subjectType,
			SubjectID:   subjectIDFor(subjectType, 0),
		}
		bindTaskMarker(ctx, deps, wsID, body, &params)
		res, ispec := ingestSignal(ctx, deps, ingestInput{
			Params:   params,
			Actor:    mutationlog.Actor{WorkspaceID: wsID},
			CallSite: "signals.HandleGithubWebhook",
		})
		if ispec != nil {
			writeError(w, ispec)
			return
		}
		if res.Duplicate {
			writeIngestDuplicate(w, res)
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":     res.PublicID.String(),
			"linked": params.TaskID.Valid,
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

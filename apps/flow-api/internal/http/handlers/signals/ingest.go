package signals

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// taskMarker is the inline body marker convention that lets an inbound
// webhook body announce which task it relates to. Example:
//
//	tnk:01931c2c-7e3a-7c66-9b85-3a0c9b3a8eaa
const taskMarker = "tnk:"

// extractTaskMarker returns the UUID of the first "tnk:" occurrence in
// body. Only that occurrence is considered: if the 36 characters after
// it are not a UUID the result is "no marker", even when a later
// occurrence is well-formed. It deliberately avoids regular expressions;
// the marker is fixed-shape (4 chars + 36 chars).
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

// bindTaskMarker links p to the task named by a body marker, resolved
// only inside wsID so a marker can never name another tenant's task. An
// unresolvable marker leaves p untouched.
func bindTaskMarker(ctx context.Context, deps Deps, wsID uint32, body []byte, p *generated.InsertSignalParams) {
	marker, ok := extractTaskMarker(body)
	if !ok {
		return
	}
	id, found, err := resolveTaskInWorkspace(ctx, deps.DB, wsID, marker)
	if err != nil || !found {
		return
	}
	p.TaskID = subjectIDFor(generated.SignalsSubjectTypeTask, id)
	p.SubjectType = generated.SignalsSubjectTypeTask
	p.SubjectID = p.TaskID
}

// ingestInput is a fully assembled signal. Params.PublicID is minted by
// ingestSignal.
type ingestInput struct {
	Params   generated.InsertSignalParams
	Actor    mutationlog.Actor
	CallSite string
}

// ingestResult carries the public id of the signal row. Duplicate is true
// when the delivery collided with an existing row and PublicID is that
// row's id.
type ingestResult struct {
	PublicID  types.PublicID
	Duplicate bool
}

// ingestSignal is the single write path for signals: insert, duplicate
// detection, the signal.attached record, then the judge wake-up.
// Constraint re-evaluation is driven by the signal.attached subscriber.
func ingestSignal(ctx context.Context, deps Deps, in ingestInput) (ingestResult, *apierrors.Spec) {
	p := in.Params
	p.PublicID = types.New()

	// Retry on transient FK deadlocks; signals shares FK lock space with tasks/workspaces.
	var signalInternalID int64
	if err := dbretry.Do(ctx, "signals.ingest", func(ctx context.Context) error {
		id, e := deps.Queries.InsertSignal(ctx, p)
		if e == nil {
			signalInternalID = id
		}
		return e
	}); err != nil {
		return ingestResult{}, apierrors.InternalUnexpected
	}

	// LastInsertId 0 means INSERT IGNORE hit the dedupe key and wrote nothing.
	if signalInternalID == 0 {
		existing, found, err := resolveSignalByDedupeKey(ctx, deps.DB, p.WorkspaceID, p.Source, p.ExternalID)
		if err != nil {
			slog.ErrorContext(ctx, "signal ingest: dedupe key lookup failed",
				slog.Any("error", err),
				slog.String("handler", in.CallSite),
			)
			return ingestResult{}, apierrors.InternalUnexpected
		}
		if !found {
			slog.ErrorContext(ctx, "signal ingest: insert wrote no row",
				slog.String("handler", in.CallSite),
				slog.Bool("external_id_present", p.ExternalID.Valid),
			)
			return ingestResult{}, apierrors.InternalUnexpected
		}
		return ingestResult{PublicID: existing, Duplicate: true}, nil
	}

	var taskPtr *int64
	if p.TaskID.Valid {
		tid := int64(p.TaskID.Int32)
		taskPtr = &tid
	}
	deps.Mutations.Record(ctx, in.Actor, mutationlog.Mutation{
		EventType:    eventbus.SignalAttached,
		AuditAction:  "signal.create",
		ResourceType: "signal",
		ResourceID:   p.PublicID.String(),
		TaskID:       taskPtr,
		Payload: map[string]any{
			"signalId": p.PublicID.String(),
			"source":   string(p.Source),
			"kind":     p.Kind,
		},
		CallSite: in.CallSite,
	})

	// Best-effort: the signal row is the canonical write.
	if deps.JudgeEnqueuer != nil {
		if err := deps.JudgeEnqueuer.EnqueueForSignal(ctx, signalInternalID, p.WorkspaceID, signalkinds.Kind(p.Kind)); err != nil {
			slog.WarnContext(ctx, "signaljudge enqueue failed",
				slog.Any("err", err),
				slog.String("handler", in.CallSite),
				slog.String("signal_public_id", p.PublicID.String()),
				slog.String("kind", p.Kind),
			)
		}
	}
	return ingestResult{PublicID: p.PublicID}, nil
}

// writeIngestDuplicate acknowledges a redelivery with the same 202 as a
// genuine insert, since providers retry non-2xx forever, but returns the
// existing row's id and marks it as a duplicate.
func writeIngestDuplicate(w http.ResponseWriter, res ingestResult) {
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":        res.PublicID.String(),
		"duplicate": true,
	})
}

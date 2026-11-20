package projects

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Archive returns a handler for POST /projects/{prjId}/archive.
func Archive(deps Deps) func(context.Context, *ArchiveProjectInput) (*ArchiveProjectOutput, error) {
	return func(ctx context.Context, _ *ArchiveProjectInput) (*ArchiveProjectOutput, error) {
		err := setArchived(ctx, deps, "projects.Archive", "project.archive",
			func(ctx context.Context, q *generated.Queries, wsID uint32, pid types.PublicID) (int64, error) {
				return q.ArchiveProject(ctx, generated.ArchiveProjectParams{WorkspaceID: wsID, PublicID: pid})
			})
		if err != nil {
			return nil, err
		}
		out := &ArchiveProjectOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Unarchive returns a handler for POST /projects/{prjId}/unarchive.
func Unarchive(deps Deps) func(context.Context, *UnarchiveProjectInput) (*UnarchiveProjectOutput, error) {
	return func(ctx context.Context, _ *UnarchiveProjectInput) (*UnarchiveProjectOutput, error) {
		err := setArchived(ctx, deps, "projects.Unarchive", "project.unarchive",
			func(ctx context.Context, q *generated.Queries, wsID uint32, pid types.PublicID) (int64, error) {
				return q.UnarchiveProject(ctx, generated.UnarchiveProjectParams{WorkspaceID: wsID, PublicID: pid})
			})
		if err != nil {
			return nil, err
		}
		out := &UnarchiveProjectOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// setArchived runs one archive-state statement in a transaction and, when it
// changed a row, records the audit entry after commit.
func setArchived(
	ctx context.Context,
	deps Deps,
	callSite, action string,
	exec func(context.Context, *generated.Queries, uint32, types.PublicID) (int64, error),
) error {
	ws, ok := middleware.WorkspaceFromContext(ctx)
	if !ok {
		return httpErr(apierrors.WsWorkspaceNotFound)
	}
	prj, ok := middleware.ProjectFromContext(ctx)
	if !ok {
		return httpErr(apierrors.WsProjectNotFound)
	}
	actorID, hasActor := middleware.ActorFromContext(ctx)

	// The statement matches only a project not already in the target state,
	// so a repeat or a losing concurrent call counts zero and records
	// nothing: there is no change to describe.
	var rows int64
	if err := dbretry.InTx(ctx, deps.DB, callSite, nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var err error
		rows, err = exec(ctx, generated.New(tx), ws.ID, types.FromUUID(prj.PublicID))
		return err
	}); err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}

	if rows > 0 && hasActor {
		deps.Audit.Record(ctx, audit.Entry{
			Action:       action,
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "project",
			ResourceID:   prj.PublicID.String(),
			Metadata:     map[string]any{"projectPublicId": prj.PublicID.String()},
		})
	}
	return nil
}

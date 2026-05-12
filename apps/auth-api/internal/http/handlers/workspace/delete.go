package workspace

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/teardown"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Delete handles DELETE /workspaces/{wsId}, the workspace owner's
// self-service immediate destructive delete.
//
// Contract:
//
//   - Owner role required (enforced upstream by RequireWorkspaceRole).
//   - Body MUST be {"confirm": true}; missing or false returns 400
//     WORKSPACE.DELETE.CONFIRM_REQUIRED. The action is irreversible —
//     the prompt-and-confirm gate is the only safety against accidental
//     destruction.
//   - Pipeline: bulk-delete every storage_objects blob from MinIO
//     (best-effort, failures logged + counted), then CASCADE-anchored
//     hard DELETE on the workspaces row. See [teardown.Workspace] for the
//     full rationale.
//   - Idempotent: if a concurrent delete won the race (RowsAffected == 0),
//     the response is 200 with deleted=false rather than 404.
//   - Suspension (PATCH with enabled=false) is a separate, reversible
//     operation and is NOT a precondition for this endpoint.
func Delete(deps Deps) func(context.Context, *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
	return func(ctx context.Context, in *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
		if in.Body.Confirm == nil || !*in.Body.Confirm {
			return nil, httpErr(apierrors.WorkspaceDeleteConfirmRequired)
		}

		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// The workspace context already carries the resolved internal id
		// from RequireWorkspaceMember, so we skip the
		// AdminFindWorkspaceIdByPublicId round-trip here.
		res, err := teardown.Workspace(ctx, deps.DB, deps.Queries, deps.Storage, ws.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Only audit when the row was actually removed; a 0-rows result
		// means a concurrent delete won the race, which is not interesting
		// for the audit timeline.
		if res.Deleted {
			if actorID, ok := authn.ActorFromContext(ctx); ok {
				// Omit WorkspaceID so the recorder routes this entry to
				// instance_audit_logs (FK SET NULL on workspace delete);
				// audit_logs has FK CASCADE and the row is already gone.
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "workspace.delete",
					ActorID:      actorID,
					ResourceType: "workspace",
					ResourceID:   ws.PublicID.String(),
					Metadata: map[string]any{
						"storage_objects_deleted": res.StorageObjectsDeleted,
						"minio_errors":            res.MinioErrors,
					},
				})
			}
		}

		out := &DeleteWorkspaceOutput{}
		out.Body.Deleted = res.Deleted
		out.Body.StorageObjectsDeleted = res.StorageObjectsDeleted
		out.Body.MinioErrors = res.MinioErrors
		return out, nil
	}
}

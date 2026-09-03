//nolint:revive // DTO names intentionally keep admin prefixes for stable generated OpenAPI schema names.
package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/teardown"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// DeleteWorkspaceInput binds the path + body for
// DELETE /admin/workspaces/{wsId}.
type DeleteWorkspaceInput struct {
	WsID string `path:"wsId"`
	Body AdminDeleteWorkspaceInputBody
}

// AdminDeleteWorkspaceInputBody is the JSON body for
// DELETE /admin/workspaces/{wsId}. See the workspace owner self-delete
// types for why Confirm is *bool (distinguish missing from explicit false).
type AdminDeleteWorkspaceInputBody struct {
	Confirm *bool `json:"confirm,omitempty" doc:"Must be true to acknowledge irreversible deletion"`
}

// DeleteWorkspaceOutput is the response for DELETE /admin/workspaces/{wsId}.
type DeleteWorkspaceOutput struct {
	Body AdminDeleteOutputBody
}

// DeleteUserInput binds the path + body for DELETE /admin/users/{userId}.
type DeleteUserInput struct {
	UserID string `path:"userId"`
	Body   DeleteUserInputBody
}

// DeleteUserInputBody is the JSON body for DELETE /admin/users/{userId}.
type DeleteUserInputBody struct {
	Confirm *bool `json:"confirm,omitempty" doc:"Must be true to acknowledge irreversible deletion"`
}

// DeleteUserOutput is the response for DELETE /admin/users/{userId}.
type DeleteUserOutput struct {
	Body AdminDeleteOutputBody
}

// AdminDeleteOutputBody is the shared response payload for both admin
// delete endpoints. Deleted=false means a concurrent delete won the race
// (RowsAffected == 0) and the response is still 200 for idempotency.
// StorageObjectsDeleted is the count of MinIO keys the driver attempted
// to delete; MinioErrors is 1 when at least one of those deletions failed
// (warning logged, DB delete still proceeded).
type AdminDeleteOutputBody struct {
	Deleted               bool  `json:"deleted"`
	StorageObjectsDeleted int64 `json:"storageObjectsDeleted"`
	MinioErrors           int64 `json:"minioErrors"`
}

// DeleteWorkspace handles DELETE /admin/workspaces/{wsId}, the admin
// force-delete counterpart to the workspace owner self-service endpoint.
//
// Behavior is identical to /workspaces/{wsId} except:
//
//   - The path resolves through AdminFindWorkspaceIdByPublicId rather
//     than the workspace middleware, so admins can force-delete
//     workspaces they are not a member of.
//   - The audit action is "admin.workspace.delete".
//
// Suspension (PATCH with enabled=false) is a separate, reversible
// operation and is NOT a precondition.
func DeleteWorkspace(deps Deps) func(context.Context, *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
	return func(ctx context.Context, in *DeleteWorkspaceInput) (*DeleteWorkspaceOutput, error) {
		if in.Body.Confirm == nil || !*in.Body.Confirm {
			return nil, httpErr(apierrors.WorkspaceDeleteConfirmRequired)
		}

		actorID, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.WsID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceWorkspaceNotFound)
		}

		wsID, err := deps.Queries.AdminFindWorkspaceIdByPublicId(ctx, pid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Already gone (never existed or a concurrent delete won
				// the race). Treat as idempotent success so the admin UI
				// can safely retry.
				out := &DeleteWorkspaceOutput{}
				return out, nil
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		res, err := teardown.Workspace(ctx, deps.DB, deps.Queries, deps.Storage, wsID, pid.UUID())
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if res.Deleted {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "admin.workspace.delete",
				ActorID:      actorID,
				ResourceType: "workspace",
				ResourceID:   in.WsID,
				Metadata: map[string]any{
					"storage_objects_deleted": res.StorageObjectsDeleted,
					"minio_errors":            res.MinioErrors,
				},
			})
		}

		out := &DeleteWorkspaceOutput{}
		out.Body.Deleted = res.Deleted
		out.Body.StorageObjectsDeleted = res.StorageObjectsDeleted
		out.Body.MinioErrors = res.MinioErrors
		return out, nil
	}
}

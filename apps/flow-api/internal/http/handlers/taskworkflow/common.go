package taskworkflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

var (
	httpErr      = handlerutil.HTTPErr
	errResponded = errors.New("task workflow request already answered")
)

func workflowContext(ctx context.Context) (middleware.WorkspaceContext, middleware.TaskContext, uint32, error) {
	ws, ok := middleware.WorkspaceFromContext(ctx)
	if !ok {
		return middleware.WorkspaceContext{}, middleware.TaskContext{}, 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	task, ok := middleware.TaskFromContext(ctx)
	if !ok {
		return middleware.WorkspaceContext{}, middleware.TaskContext{}, 0, httpErr(apierrors.WsTaskNotFound)
	}
	actorID, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return middleware.WorkspaceContext{}, middleware.TaskContext{}, 0, httpErr(apierrors.WsWorkspaceAccessDenied)
	}
	return ws, task, actorID, nil
}

func publicID(raw string) (types.PublicID, error) {
	return types.Parse(raw)
}

func authorizeTaskReader(ctx context.Context, db acl.DB, taskID uuid.UUID, userID uint32) error {
	_, err := acl.AuthorizeTaskAccess(ctx, db, taskID, userID, apierrors.WsTaskAccessDenied)
	if err == nil {
		return nil
	}
	var ae *apierr.APIError
	if errors.As(err, &ae) {
		return handlerutil.HTTPErrFromAPIError(ae)
	}
	return httpErr(apierrors.InternalUnexpected)
}

func lockTask(ctx context.Context, qtx *generated.Queries, wsID, taskID uint32) error {
	_, err := qtx.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
		ID:          taskID,
		WorkspaceID: wsID,
	})
	return err
}

func recordWorkflowEvent(
	ctx context.Context,
	tx *dbretry.Tx,
	deps Deps,
	actorID, workspaceID uint32,
	taskID uint32,
	resourceID string,
	event eventbus.Kind,
	auditAction, resourceType, callSite string,
	payload map[string]any,
) error {
	taskInternal := int64(taskID)
	return deps.Events.RecordInTx(ctx, tx,
		mutationlog.Actor{UserID: actorID, WorkspaceID: workspaceID},
		mutationlog.Mutation{
			EventType:    event,
			AuditAction:  auditAction,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			TaskID:       &taskInternal,
			Payload:      payload,
			CallSite:     callSite,
		},
	)
}

func refreshTaskConstraints(ctx context.Context, q *generated.Queries, workspaceID, taskID uint32) error {
	eng := &engine.Engine{Store: &engine.SqlcStore{WorkspaceID: workspaceID, Queries: q}}
	return eng.EvaluateTaskAndDependents(ctx, taskID)
}

func isElevatedWorkspaceRole(role middleware.WorkspaceRole) bool {
	return role == middleware.WorkspaceRoleAdmin || role == middleware.WorkspaceRoleOwner
}

func nullableUserID(id uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- user ids are bounded by the database's INT UNSIGNED key and production rows fit int32
}

func parseTaskUUID(id types.PublicID) uuid.UUID { return id.UUID() }

func dbString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

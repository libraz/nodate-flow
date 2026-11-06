package sourceintake

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// Linkable reports whether an unconverted intake may still be joined to a
// task: pending, accepted without a task, or snoozed past its deadline. It
// mirrors the status predicate of SetIntakeItemTask and
// LinkIntakeItemToExistingTask, which remain the authoritative claim.
func Linkable(status generated.IntakeItemsTriageStatus, snoozeUntil sql.NullTime) bool {
	switch status {
	case generated.IntakeItemsTriageStatusPending, generated.IntakeItemsTriageStatusAccepted:
		return true
	case generated.IntakeItemsTriageStatusSnoozed:
		return snoozeUntil.Valid && !snoozeUntil.Time.After(time.Now())
	default:
		return false
	}
}

// LinkTarget is an existing task locked and authorized inside the caller's
// transaction for an intake or signal link.
type LinkTarget struct {
	Task      acl.TaskRecord
	PublicID  types.PublicID
	ProjectID types.PublicID
}

// LockLinkTarget locks the enabled task named by taskPub and re-checks, with
// locking reads, that userID may see it and edit its project. Callers lock
// the signal and intake rows first. Denials are API errors: a task outside
// the workspace or hidden from the caller is WS.TASK.NOT_FOUND, a missing
// editor grant WS.PROJECT.ACCESS_DENIED.
func LockLinkTarget(ctx context.Context, tx acl.DB, workspaceID, userID uint32, taskPub types.PublicID) (LinkTarget, error) {
	rec, err := acl.ResolveTaskByPublicIDForUpdate(ctx, tx, taskPub.UUID())
	if err != nil {
		return LinkTarget{}, err
	}
	if rec.WorkspaceID != workspaceID {
		return LinkTarget{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	wsRole, err := acl.CheckWorkspaceMemberForShare(ctx, tx, workspaceID, userID, apierrors.WsTaskAccessDenied)
	if err != nil {
		return LinkTarget{}, err
	}
	prjRole, isProjectMember, err := acl.LookupProjectMembershipForShare(ctx, tx, workspaceID, rec.ProjectID, userID, wsRole)
	if err != nil {
		return LinkTarget{}, err
	}
	if err := acl.CheckTaskVisibilityForShare(ctx, tx, rec, userID, wsRole, isProjectMember); err != nil {
		return LinkTarget{}, err
	}
	if prjRole != acl.ProjectRoleElevated && !prjRole.AtLeast(acl.ProjectRoleEditor) {
		return LinkTarget{}, apierrors.New(apierrors.WsProjectAccessDenied)
	}
	prjPub, err := acl.ResolveProjectPublicByID(ctx, tx, rec.ProjectID, apierrors.WsTaskNotFound)
	if err != nil {
		return LinkTarget{}, err
	}
	return LinkTarget{Task: rec, PublicID: taskPub, ProjectID: prjPub}, nil
}

// LinkedTask resolves the public id of a task an intake or signal already
// points at, refusing one userID can no longer see. nonMemberSpec is the
// transport's disclosure posture, as for [acl.AuthorizeTaskAccess].
func LinkedTask(
	ctx context.Context,
	db acl.DB,
	q *generated.Queries,
	workspaceID, userID uint32,
	taskID int32,
	nonMemberSpec *apierrors.Spec,
) (types.PublicID, error) {
	taskPub, err := q.FindPublicIdForIntakeTask(ctx, generated.FindPublicIdForIntakeTaskParams{
		WorkspaceID: workspaceID,
		ID:          uint32(taskID), //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
		}
		return types.PublicID{}, err
	}
	access, err := acl.AuthorizeTaskAccess(ctx, db, taskPub.UUID(), userID, nonMemberSpec)
	if err != nil {
		return types.PublicID{}, err
	}
	if access.Task.WorkspaceID != workspaceID {
		return types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	return taskPub, nil
}

// AcceptedPayload is the intake.item.accepted payload every transport emits
// when an intake is joined to a task. signalID is omitted for a manual
// intake, which has no source signal.
func AcceptedPayload(intakeItemID, taskID, projectID, signalID types.PublicID) map[string]any {
	payload := map[string]any{
		"intakeItemId": intakeItemID.String(),
		"taskId":       taskID.String(),
		"projectId":    projectID.String(),
	}
	if signalID != (types.PublicID{}) {
		payload["signalId"] = signalID.String()
	}
	return payload
}

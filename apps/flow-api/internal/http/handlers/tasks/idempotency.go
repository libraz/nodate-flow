package tasks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/operation"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const taskMutationTargetKind = "task"

// taskMutationTxOptions gives receipt-bearing commands a fresh READ
// COMMITTED snapshot after waiting for the key lock. This lets authorization
// observe membership changes committed while another request held that lock.
func taskMutationTxOptions(key string) *sql.TxOptions {
	if key == "" {
		return nil
	}
	return &sql.TxOptions{Isolation: sql.LevelReadCommitted}
}

// lockTaskMutation serializes one task mutation key, rechecks current user
// authorization in the transaction, and loads a previously committed result
// into response on replay. SQL errors remain unwrapped so dbretry can retry
// deadlocks and other transient failures.
func lockTaskMutation(
	ctx context.Context,
	tx *dbretry.Tx,
	workspace middleware.WorkspaceContext,
	task middleware.TaskContext,
	actorID uint32,
	capability execution.Capability,
	kind, key string,
	body, response any,
) (operation.Receipt, bool, error) {
	if key == "" {
		return operation.Receipt{}, false, nil
	}
	if tx == nil || workspace.ID == 0 || task.ID == 0 || actorID == 0 || kind == "" ||
		!validTaskIdempotencyKey(key) || response == nil {
		return operation.Receipt{}, false, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}

	requestHash, err := taskMutationRequestHash(task.PublicID.String(), body)
	if err != nil {
		return operation.Receipt{}, false, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	principal, err := execution.NewUserPrincipal(workspace.ID, actorID)
	if err != nil {
		return operation.Receipt{}, false, apierrors.New(apierrors.InternalUnexpected)
	}

	receipt, err := operation.Lock(ctx, tx, operation.Request{
		WorkspaceID:    workspace.ID,
		Principal:      operation.Principal{Kind: operation.PrincipalUser, ID: actorID},
		Kind:           kind,
		Key:            key,
		TargetKind:     taskMutationTargetKind,
		TargetPublicID: types.FromUUID(task.PublicID),
		Hash:           requestHash,
	})
	if err != nil {
		if errors.Is(err, operation.ErrInvalidRequest) {
			return operation.Receipt{}, false, apierrors.New(apierrors.ValidationBodyFieldInvalid)
		}
		return operation.Receipt{}, false, err
	}

	authorization, err := execution.AuthorizeTaskTx(
		ctx,
		tx,
		principal,
		types.FromUUID(task.PublicID),
		capability,
	)
	if err != nil {
		if errors.Is(err, execution.ErrTaskOutsideWorkspace) {
			return operation.Receipt{}, false, apierrors.New(apierrors.WsTaskNotFound)
		}
		if errors.Is(err, execution.ErrInvalidPrincipal) {
			return operation.Receipt{}, false, apierrors.New(apierrors.InternalUnexpected)
		}
		return operation.Receipt{}, false, err
	}
	if authorization.Task.ID != task.ID {
		return operation.Receipt{}, false, apierrors.New(apierrors.WsTaskNotFound)
	}
	var requiredRole acl.ProjectRole
	switch capability {
	case execution.CapabilityAddComment:
		requiredRole = acl.ProjectRoleCommenter
	case execution.CapabilityEditTask:
		requiredRole = acl.ProjectRoleEditor
	default:
		return operation.Receipt{}, false, apierrors.New(apierrors.InternalUnexpected)
	}
	if authorization.ProjectRole != acl.ProjectRoleElevated && !authorization.ProjectRole.AtLeast(requiredRole) {
		return operation.Receipt{}, false, apierrors.New(apierrors.WsProjectAccessDenied)
	}

	if receipt.Hash != requestHash {
		return operation.Receipt{}, false, apierrors.New(apierrors.WsTaskIdempotencyKeyConflict)
	}
	if receipt.CompletedAt == nil {
		return receipt, false, nil
	}
	if err := json.Unmarshal(receipt.Result, response); err != nil {
		return operation.Receipt{}, false, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	return receipt, true, nil
}

// completeTaskMutation persists the exact response body beside the mutation.
// A zero receipt means the caller did not supply an idempotency key.
func completeTaskMutation(ctx context.Context, tx *dbretry.Tx, receipt operation.Receipt, response any) error {
	if receipt.ID == (types.PublicID{}) {
		return nil
	}
	result, err := json.Marshal(response)
	if err != nil {
		return apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	if err := operation.Complete(ctx, tx, receipt, result); err != nil {
		return err
	}
	return nil
}

// taskMutationHTTPError converts typed domain errors into the standard Huma
// envelope. It should be called only after dbretry has exhausted its retry
// attempts, so raw SQL errors can still be recognized by the retry loop.
func taskMutationHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var statusErr huma.StatusError
	if errors.As(err, &statusErr) {
		return err
	}
	var apiError *apierr.APIError
	if errors.As(err, &apiError) {
		return handlerutil.HTTPErrFromAPIError(apiError)
	}
	return httpErr(apierrors.InternalUnexpected)
}

func validTaskIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			strings.ContainsRune("._~-", char) {
			continue
		}
		return false
	}
	return true
}

func taskMutationRequestHash(taskID string, body any) ([32]byte, error) {
	canonical, err := json.Marshal(struct {
		TaskID string `json:"taskId"`
		Body   any    `json:"body"`
	}{TaskID: taskID, Body: body})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

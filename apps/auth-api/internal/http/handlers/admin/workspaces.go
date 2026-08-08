package admin

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// ListWorkspaces handles GET /admin/workspaces. Returns a paginated list of
// all workspaces with optional search and enabled-status filtering.
func ListWorkspaces(deps Deps) func(context.Context, *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
	return func(ctx context.Context, in *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		var filterEnabled interface{}
		var enabledBool bool
		if in.Enabled != "" {
			enabledBool = in.Enabled == "true"
			filterEnabled = enabledBool
		}

		rows, err := deps.Queries.AdminListWorkspaces(ctx, generated.AdminListWorkspacesParams{
			Column1:  in.Search,
			CONCAT:   in.Search,
			CONCAT_2: in.Search,
			Column4:  filterEnabled,
			Enabled:  enabledBool,
			Limit:    limit,
			Offset:   in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListWorkspacesOutput{}
		out.Body.Items = make([]AdminWorkspace, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToAdminWorkspaceList(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// GetWorkspace handles GET /admin/workspaces/{wsId}. Returns the detail view
// for a single workspace identified by public id, including project count.
func GetWorkspace(deps Deps) func(context.Context, *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
	return func(ctx context.Context, in *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
		pid, err := types.Parse(in.WsID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceWorkspaceNotFound)
		}

		row, err := deps.Queries.AdminGetWorkspace(ctx, pid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceWorkspaceNotFound, apierrors.InternalUnexpected))
		}

		return &GetWorkspaceOutput{Body: rowToAdminWorkspaceDetail(row)}, nil
	}
}

// PatchWorkspace handles PATCH /admin/workspaces/{wsId}. Currently supports
// toggling the enabled flag to suspend or re-enable a workspace.
func PatchWorkspace(deps Deps) func(context.Context, *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
	return func(ctx context.Context, in *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
		uid, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.WsID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceWorkspaceNotFound)
		}

		if in.Body.Enabled == nil {
			out := &PatchWorkspaceOutput{}
			out.Body.Ok = true
			return out, nil
		}

		// See PatchUser: a zero count means either "no such workspace" or
		// "already in the requested state", and only the first is an
		// error. Answering success for a workspace id that does not exist
		// left an audit entry claiming a workspace had been suspended.
		var rows int64
		if *in.Body.Enabled {
			rows, err = deps.Queries.AdminEnableWorkspace(ctx, pid)
		} else {
			rows, err = deps.Queries.AdminSuspendWorkspace(ctx, pid)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			if _, getErr := deps.Queries.AdminGetWorkspace(ctx, pid); getErr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(getErr, apierrors.InstanceWorkspaceNotFound, apierrors.InternalUnexpected))
			}
			out := &PatchWorkspaceOutput{}
			out.Body.Ok = true
			return out, nil
		}

		action := "admin.workspace.enable"
		if !*in.Body.Enabled {
			action = "admin.workspace.suspend"
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       action,
			ActorID:      uid,
			ResourceType: "workspace",
			ResourceID:   in.WsID,
		})

		out := &PatchWorkspaceOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// ErrTaskOutsideWorkspace is deliberately distinct from a task visibility
// denial. REST and MCP adapters can preserve their existing disclosure
// choices without weakening the shared authorization decision.
var ErrTaskOutsideWorkspace = errors.New("execution: task belongs to a different workspace")

// Capability is the closed set of task operations the execution layer authorizes.
type Capability string

const (
	CapabilityReadTask    Capability = "read_task"
	CapabilitySetPriority Capability = "set_priority"
	CapabilityAddComment  Capability = "add_comment"
)

// AgentIdentity contains the current public agent identity needed for
// attribution. Internal ID remains server-side and is never placed in
// event payloads or API DTOs.
type AgentIdentity struct {
	ID       uint32
	PublicID types.PublicID
	Name     string
}

// TaskAuthorization is the transaction-current result of authorizing one
// task operation. Taskservice must use Task from this result rather than
// resolving the target again outside the transaction.
type TaskAuthorization struct {
	Task                acl.TaskRecord
	WorkspaceRole       acl.WorkspaceRole
	ProjectRole         acl.ProjectRole
	IsProjectMember     bool
	Agent               *AgentIdentity
	DelegatedByPublicID types.PublicID
}

type requirement struct {
	tool  string
	scope string
	floor acl.ProjectRole
}

func requirementFor(capability Capability) (requirement, error) {
	switch capability {
	case CapabilityReadTask:
		return requirement{tool: ToolReadTask, scope: ScopeReadWorkspace}, nil
	case CapabilitySetPriority:
		return requirement{tool: ToolSetPriority, scope: ScopeWriteWorkspace, floor: acl.ProjectRoleEditor}, nil
	case CapabilityAddComment:
		return requirement{tool: ToolAddComment, scope: ScopeWriteWorkspace, floor: acl.ProjectRoleCommenter}, nil
	default:
		return requirement{}, fmt.Errorf("%w: unsupported capability %q", ErrInvalidPrincipal, capability)
	}
}

// AuthorizeTaskTx locks the current task first, then acquires current
// locking reads for the human, agent, assignment, and project authority
// rows needed by the requested capability. The caller must keep all work
// that follows on tx and commit the authorization and any write together.
func AuthorizeTaskTx(
	ctx context.Context,
	tx *dbretry.Tx,
	principal Principal,
	target types.PublicID,
	capability Capability,
) (TaskAuthorization, error) {
	if err := principal.Validate(); err != nil || tx == nil || target == (types.PublicID{}) {
		return TaskAuthorization{}, ErrInvalidPrincipal
	}
	if err := acl.EnforceTokenWorkspace(ctx, principal.WorkspaceID()); err != nil {
		return TaskAuthorization{}, err
	}
	req, err := requirementFor(capability)
	if err != nil {
		return TaskAuthorization{}, err
	}
	if principal.IsAgent() && !principal.CapturedTarget(target) {
		return TaskAuthorization{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	db := tx.RawTx()
	rec, err := acl.ResolveTaskByPublicIDForUpdate(ctx, db, uuid.UUID(target.UUID()))
	if err != nil {
		return TaskAuthorization{}, err
	}
	if rec.WorkspaceID != principal.WorkspaceID() {
		if err := acl.EnforceTokenWorkspace(ctx, rec.WorkspaceID); err != nil {
			return TaskAuthorization{}, err
		}
		return TaskAuthorization{}, ErrTaskOutsideWorkspace
	}
	if err := requireEnabledTaskParentsForShare(ctx, db, rec); err != nil {
		return TaskAuthorization{}, err
	}
	if principal.IsAgent() {
		return authorizeAgentTaskTx(ctx, db, principal, rec, target, req)
	}
	return authorizeUserTaskTx(ctx, db, principal.WorkspaceID(), principal.UserID(), rec, req)
}

// requireEnabledTaskParentsForShare locks the task's current workspace and
// project parents after the task row and before any authority rows. This
// keeps parent soft-deactivation current for both human and agent commands
// that otherwise might write directly to a child table.
func requireEnabledTaskParentsForShare(ctx context.Context, db acl.DB, rec acl.TaskRecord) error {
	var workspaceEnabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM workspaces WHERE id = ? LIMIT 1 FOR SHARE`, rec.WorkspaceID,
	).Scan(&workspaceEnabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		return err
	}
	if !workspaceEnabled {
		return apierrors.New(apierrors.WsTaskNotFound)
	}

	var projectEnabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM projects WHERE id = ? AND workspace_id = ? LIMIT 1 FOR SHARE`, rec.ProjectID, rec.WorkspaceID,
	).Scan(&projectEnabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		return err
	}
	if !projectEnabled {
		return apierrors.New(apierrors.WsTaskNotFound)
	}
	return nil
}

func authorizeUserTaskTx(
	ctx context.Context,
	db acl.DB,
	workspaceID, userID uint32,
	rec acl.TaskRecord,
	req requirement,
) (TaskAuthorization, error) {
	if rec.WorkspaceID != workspaceID {
		return TaskAuthorization{}, ErrTaskOutsideWorkspace
	}
	wsRole, err := acl.CheckWorkspaceMemberForShare(ctx, db, rec.WorkspaceID, userID, apierrors.WsTaskAccessDenied)
	if err != nil {
		return TaskAuthorization{}, err
	}
	projectRole, isProjectMember, err := acl.LookupProjectMembershipForShare(ctx, db, rec.WorkspaceID, rec.ProjectID, userID, wsRole)
	if err != nil {
		return TaskAuthorization{}, err
	}
	if err := acl.CheckTaskVisibilityForShare(ctx, db, rec, userID, wsRole, isProjectMember); err != nil {
		return TaskAuthorization{}, err
	}
	if req.floor != "" && !wsRole.AtLeast(acl.WorkspaceRoleAdmin) && !projectRole.AtLeast(req.floor) {
		return TaskAuthorization{}, apierrors.New(apierrors.WsProjectAccessDenied)
	}
	return TaskAuthorization{
		Task:            rec,
		WorkspaceRole:   wsRole,
		ProjectRole:     projectRole,
		IsProjectMember: isProjectMember,
	}, nil
}

func authorizeAgentTaskTx(
	ctx context.Context,
	db acl.DB,
	p Principal,
	rec acl.TaskRecord,
	_ types.PublicID,
	req requirement,
) (TaskAuthorization, error) {
	if !p.HasTool(req.tool) || !p.HasScope(req.scope) {
		return TaskAuthorization{}, apierrors.New(apierrors.McpScopeInsufficient)
	}
	if rec.Visibility != acl.TaskVisibilityPublic {
		return TaskAuthorization{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	agent, err := loadCurrentAgentForShare(ctx, db, p.WorkspaceID(), p.AgentID(), req)
	if err != nil {
		return TaskAuthorization{}, err
	}
	assigned, err := acl.IsAgentTaskAssigneeForShare(ctx, db, rec.ID, p.AgentID())
	if err != nil {
		return TaskAuthorization{}, err
	}
	if !assigned {
		return TaskAuthorization{}, apierrors.New(apierrors.WsTaskNotFound)
	}

	authz := TaskAuthorization{Task: rec, Agent: &agent}
	if delegatorID := p.DelegatedByUserID(); delegatorID != 0 {
		delegator, err := authorizeUserTaskTx(ctx, db, p.WorkspaceID(), delegatorID, rec, req)
		if err != nil {
			return TaskAuthorization{}, err
		}
		authz.DelegatedByPublicID, err = loadUserPublicID(ctx, db, delegatorID)
		if err != nil {
			return TaskAuthorization{}, err
		}
		authz.WorkspaceRole = delegator.WorkspaceRole
		authz.ProjectRole = delegator.ProjectRole
		authz.IsProjectMember = delegator.IsProjectMember
	}
	return authz, nil
}

func loadCurrentAgentForShare(
	ctx context.Context,
	db acl.DB,
	workspaceID, agentID uint32,
	req requirement,
) (AgentIdentity, error) {
	const q = `SELECT public_id, name, enabled, paused, tools_json, allowed_scopes_json
FROM ai_agents
WHERE id = ? AND workspace_id = ?
LIMIT 1 FOR SHARE`
	var (
		identity  AgentIdentity
		enabled   bool
		paused    bool
		toolsRaw  []byte
		scopesRaw []byte
	)
	identity.ID = agentID
	if err := db.QueryRowContext(ctx, q, agentID, workspaceID).Scan(
		&identity.PublicID, &identity.Name, &enabled, &paused, &toolsRaw, &scopesRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentIdentity{}, apierrors.New(apierrors.WsTaskNotFound)
		}
		return AgentIdentity{}, err
	}
	if !enabled || paused {
		return AgentIdentity{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	var tools []string
	if len(toolsRaw) != 0 {
		if err := json.Unmarshal(toolsRaw, &tools); err != nil {
			return AgentIdentity{}, apierrors.New(apierrors.InternalUnexpected)
		}
	}
	if !contains(tools, req.tool) {
		return AgentIdentity{}, apierrors.New(apierrors.McpScopeInsufficient)
	}
	var scopes []string
	if len(scopesRaw) != 0 {
		if err := json.Unmarshal(scopesRaw, &scopes); err != nil {
			return AgentIdentity{}, apierrors.New(apierrors.InternalUnexpected)
		}
	}
	// An empty agent allowlist inherits the token scope, matching the
	// established agentguard behavior. A non-empty list narrows it.
	if len(scopes) != 0 && !hasScope(scopes, req.scope) {
		return AgentIdentity{}, apierrors.New(apierrors.McpScopeInsufficient)
	}
	return identity, nil
}

func loadUserPublicID(ctx context.Context, db acl.DB, userID uint32) (types.PublicID, error) {
	const q = `SELECT public_id FROM users WHERE id = ? LIMIT 1`
	var publicID types.PublicID
	if err := db.QueryRowContext(ctx, q, userID).Scan(&publicID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.PublicID{}, apierrors.New(apierrors.WsTaskAccessDenied)
		}
		return types.PublicID{}, err
	}
	return publicID, nil
}

func hasScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required || (required == ScopeReadWorkspace && scope == ScopeWriteWorkspace) {
			return true
		}
	}
	return false
}

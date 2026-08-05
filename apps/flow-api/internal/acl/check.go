package acl

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// DB is the minimal subset of *sql.DB that the ACL check functions
// need. Defining it as an interface keeps the check logic testable
// without a live database connection and lets any caller (HTTP
// middleware, MCP handler, future workers) plug in either *sql.DB or
// an instrumented wrapper.
type DB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ----------------------------------------------------------------------------
// Layer 1: instance
// ----------------------------------------------------------------------------

// CheckInstanceAdmin verifies the actor has an active row in
// instance_admins. Returns nil on success,
// AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED on permission failure, or the
// underlying database error for transport-level problems (callers map
// those to their transport's generic 5xx code).
func CheckInstanceAdmin(ctx context.Context, db DB, userID uint32) error {
	const q = `SELECT 1 FROM instance_admins
WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, q, userID).Scan(&one); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.New(apierrors.AuthPermissionInstanceAdminRequired)
		}
		return err
	}
	return nil
}

// ----------------------------------------------------------------------------
// Bearer token workspace binding
// ----------------------------------------------------------------------------

// TokenWorkspaceMismatch returns the error a workspace-bound bearer token
// gets when it is replayed against a workspace it was not minted for. MCP
// tokens get their own code so an MCP client can tell a binding failure
// apart from a plain membership failure.
func TokenWorkspaceMismatch(ctx context.Context) error {
	if kind, _ := authn.TokenKindFromContext(ctx); kind == authn.TokenKindMCP {
		return apierrors.New(apierrors.McpTokenWorkspaceMismatch)
	}
	return apierrors.New(apierrors.WsWorkspaceAccessDenied)
}

// EnforceTokenWorkspace verifies that the bearer token authenticating the
// request is allowed to act on workspaceID.
//
// PAT and MCP tokens are minted against exactly one workspace
// (personal_access_tokens.workspace_id / mcp_tokens.workspace_id is NOT
// NULL), so a token replayed against any other workspace must be rejected
// even when its owner is a member of both. The check lives here — inside
// the shared workspace membership gate — rather than at each call site so a
// route that resolves a workspace by a route parameter, a request body, or
// a task/project id cannot silently opt out of the binding.
//
// Requests carrying no workspace-bound token (browser JWT sessions, the
// internal service token) pass through untouched.
func EnforceTokenWorkspace(ctx context.Context, workspaceID uint32) error {
	bound, ok := authn.TokenWorkspaceIDFromContext(ctx)
	if !ok || bound == workspaceID {
		return nil
	}
	return TokenWorkspaceMismatch(ctx)
}

// ----------------------------------------------------------------------------
// Layer 2: workspace
// ----------------------------------------------------------------------------

// WorkspaceAccess is the resolved result of a successful workspace
// access check. It carries the internal id and role for downstream use.
type WorkspaceAccess struct {
	ID   uint32
	Role WorkspaceRole
}

// ResolveWorkspaceByPublicID looks up a workspace by its public id.
// Returns the internal id on success, or WS.WORKSPACE.NOT_FOUND if the
// workspace does not exist or is disabled.
func ResolveWorkspaceByPublicID(ctx context.Context, db DB, pub uuid.UUID) (uint32, error) {
	const q = `SELECT id FROM workspaces
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, types.FromUUID(pub)).Scan(&id); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.WsWorkspaceNotFound)
		}
		return 0, err
	}
	return id, nil
}

// ResolveWorkspacePublicByID looks up a workspace public id by its
// internal id. Returns WS.WORKSPACE.NOT_FOUND if missing.
func ResolveWorkspacePublicByID(ctx context.Context, db DB, id uint32) (types.PublicID, error) {
	const q = `SELECT public_id FROM workspaces
WHERE id = ? AND enabled = TRUE LIMIT 1`
	var pub types.PublicID
	if err := db.QueryRowContext(ctx, q, id).Scan(&pub); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return types.PublicID{}, apierrors.New(apierrors.WsWorkspaceNotFound)
		}
		return types.PublicID{}, err
	}
	return pub, nil
}

// CheckWorkspaceMember verifies the actor is an enabled member of the
// workspace and returns their role. Returns WS.WORKSPACE.ACCESS_DENIED
// when the actor is not a member.
//
// The deniedSpec parameter lets callers override the access-denied
// error when the workspace is being resolved indirectly (for example
// via a project or task lookup) so the leaked existence is consistent
// with the outer resource's not-found behaviour.
//
// The role column is a MySQL ENUM. We re-validate against the Go
// [WorkspaceRole] enum as defence in depth: a row carrying an unknown
// role string surfaces as INTERNAL.UNEXPECTED, never as a 403. A
// corrupt enum is a server-side invariant violation, not a caller
// permissions failure.
func CheckWorkspaceMember(ctx context.Context, db DB, wsID, userID uint32, deniedSpec *apierrors.Spec) (WorkspaceRole, error) {
	// A workspace-bound bearer token never gets past its own workspace,
	// whatever membership the token owner holds elsewhere.
	if err := EnforceTokenWorkspace(ctx, wsID); err != nil {
		return "", err
	}
	const q = `SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var role string
	if err := db.QueryRowContext(ctx, q, wsID, userID).Scan(&role); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			if deniedSpec == nil {
				deniedSpec = apierrors.WsWorkspaceAccessDenied
			}
			return "", apierrors.New(deniedSpec)
		}
		return "", err
	}
	wr := WorkspaceRole(role)
	if !wr.IsValid() {
		return "", apierrors.New(apierrors.InternalUnexpected)
	}
	return wr, nil
}

// ResolveWorkspaceAccess combines [ResolveWorkspaceByPublicID] and
// [CheckWorkspaceMember] into the single check the HTTP middleware
// performs at the workspace layer. It is the canonical implementation
// of the workspace ACL gate.
func ResolveWorkspaceAccess(ctx context.Context, db DB, pub uuid.UUID, userID uint32) (WorkspaceAccess, error) {
	wsID, err := ResolveWorkspaceByPublicID(ctx, db, pub)
	if err != nil {
		return WorkspaceAccess{}, err
	}
	role, err := CheckWorkspaceMember(ctx, db, wsID, userID, nil)
	if err != nil {
		return WorkspaceAccess{}, err
	}
	return WorkspaceAccess{ID: wsID, Role: role}, nil
}

// ----------------------------------------------------------------------------
// Layer 3: project
// ----------------------------------------------------------------------------

// ProjectAccess is the resolved result of a successful project access
// check. Role is empty (ProjectRoleElevated) when access was granted
// purely via workspace owner/admin elevation.
type ProjectAccess struct {
	ID   uint32
	Role ProjectRole
}

// ProjectLookup is the bare project record needed for ACL decisions:
// internal id and the workspace it belongs to.
type ProjectLookup struct {
	ID          uint32
	WorkspaceID uint32
}

// ResolveProjectInWorkspace looks up a project by public id inside a
// known workspace. Returns WS.PROJECT.NOT_FOUND when the project does
// not exist or belongs to a different workspace.
func ResolveProjectInWorkspace(ctx context.Context, db DB, wsID uint32, pub uuid.UUID) (uint32, error) {
	const q = `SELECT id FROM projects
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, wsID, types.FromUUID(pub)).Scan(&id); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.WsProjectNotFound)
		}
		return 0, err
	}
	return id, nil
}

// ResolveProjectByPublicID looks up a project globally by public id
// and returns its internal id and owning workspace id. Returns
// WS.PROJECT.NOT_FOUND when missing.
func ResolveProjectByPublicID(ctx context.Context, db DB, pub uuid.UUID) (ProjectLookup, error) {
	const q = `SELECT id, workspace_id FROM projects
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var out ProjectLookup
	if err := db.QueryRowContext(ctx, q, types.FromUUID(pub)).Scan(&out.ID, &out.WorkspaceID); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return ProjectLookup{}, apierrors.New(apierrors.WsProjectNotFound)
		}
		return ProjectLookup{}, err
	}
	return out, nil
}

// ResolveProjectPublicByID looks up a project public id by its
// internal id. Returns WS.TASK.NOT_FOUND if missing — this helper is
// only used by task-resolution paths where a missing parent project
// implies a missing task to the caller.
func ResolveProjectPublicByID(ctx context.Context, db DB, id uint32, missingSpec *apierrors.Spec) (types.PublicID, error) {
	const q = `SELECT public_id FROM projects
WHERE id = ? AND enabled = TRUE LIMIT 1`
	var pub types.PublicID
	if err := db.QueryRowContext(ctx, q, id).Scan(&pub); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			if missingSpec == nil {
				missingSpec = apierrors.WsProjectNotFound
			}
			return types.PublicID{}, apierrors.New(missingSpec)
		}
		return types.PublicID{}, err
	}
	return pub, nil
}

// CheckProjectMembership returns the actor's project role and whether
// they are a direct project member. When the actor is not a project
// member but holds an elevated workspace role (owner/admin), access is
// granted with [ProjectRoleElevated]. Otherwise this returns
// deniedSpec (default WS.PROJECT.ACCESS_DENIED).
//
// The role column is a MySQL ENUM so any value reaching this function
// is already constrained at the schema layer. We re-validate against
// the Go [ProjectRole] enum anyway (defence in depth): if a row
// somehow carries an unknown role string — schema drift, manual edit,
// future column added but not yet mapped — we return INTERNAL.UNEXPECTED
// rather than a misleading 403. A corrupt enum is a server-side
// invariant violation, not the caller's fault.
func CheckProjectMembership(
	ctx context.Context,
	db DB,
	wsID, prjID, userID uint32,
	wsRole WorkspaceRole,
	deniedSpec *apierrors.Spec,
) (role ProjectRole, isMember bool, err error) {
	const q = `SELECT role FROM project_members
WHERE workspace_id = ? AND project_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var roleStr string
	scanErr := db.QueryRowContext(ctx, q, wsID, prjID, userID).Scan(&roleStr)
	switch {
	case scanErr == nil:
		pr := ProjectRole(roleStr)
		if !pr.IsValid() || pr == ProjectRoleElevated {
			// Empty role here is a real corrupt row (the elevated marker
			// is only used for the not-a-member-but-elevated path below,
			// never for project_members rows that actually exist).
			return "", false, apierrors.New(apierrors.InternalUnexpected)
		}
		return pr, true, nil
	case stderrors.Is(scanErr, sql.ErrNoRows):
		if !wsRole.AtLeast(WorkspaceRoleAdmin) {
			if deniedSpec == nil {
				deniedSpec = apierrors.WsProjectAccessDenied
			}
			return "", false, apierrors.New(deniedSpec)
		}
		return ProjectRoleElevated, false, nil
	default:
		return "", false, scanErr
	}
}

// LookupProjectMembership returns the actor's direct project role when present.
// Workspace owners/admins are returned as [ProjectRoleElevated]. Unlike
// [CheckProjectMembership], a workspace member who is not on the project is not
// an error; callers can feed the isMember result into a higher-level visibility
// decision. Non-elevated workspace members without a project_members row get
// [ProjectRoleNone], which can read public tasks but must fail project-role
// write gates.
func LookupProjectMembership(
	ctx context.Context,
	db DB,
	wsID, prjID, userID uint32,
	wsRole WorkspaceRole,
) (role ProjectRole, isMember bool, err error) {
	const q = `SELECT role FROM project_members
WHERE workspace_id = ? AND project_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var roleStr string
	scanErr := db.QueryRowContext(ctx, q, wsID, prjID, userID).Scan(&roleStr)
	switch {
	case scanErr == nil:
		pr := ProjectRole(roleStr)
		if !pr.IsValid() || pr == ProjectRoleElevated {
			return "", false, apierrors.New(apierrors.InternalUnexpected)
		}
		return pr, true, nil
	case stderrors.Is(scanErr, sql.ErrNoRows):
		if wsRole.AtLeast(WorkspaceRoleAdmin) {
			return ProjectRoleElevated, false, nil
		}
		return ProjectRoleNone, false, nil
	default:
		return "", false, scanErr
	}
}

// ----------------------------------------------------------------------------
// Layer 4: task
// ----------------------------------------------------------------------------

// TaskRecord is the minimal task row needed for ACL decisions.
type TaskRecord struct {
	ID              uint32
	WorkspaceID     uint32
	ProjectID       uint32
	Visibility      TaskVisibility
	CreatedByUserID sql.NullInt32
}

// TaskAccess is the full Layer 3/4 authorization result for a task.
type TaskAccess struct {
	Task            TaskRecord
	WorkspaceRole   WorkspaceRole
	ProjectRole     ProjectRole
	IsProjectMember bool
}

// ResolveTaskByPublicID looks up a task by public id and returns the
// fields needed for downstream ACL decisions. Returns
// WS.TASK.NOT_FOUND when missing.
func ResolveTaskByPublicID(ctx context.Context, db DB, pub uuid.UUID) (TaskRecord, error) {
	const q = `SELECT id, workspace_id, project_id, visibility, created_by_user_id FROM tasks
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var out TaskRecord
	var visibility string
	if err := db.QueryRowContext(ctx, q, types.FromUUID(pub)).Scan(
		&out.ID, &out.WorkspaceID, &out.ProjectID, &visibility, &out.CreatedByUserID,
	); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return TaskRecord{}, apierrors.New(apierrors.WsTaskNotFound)
		}
		return TaskRecord{}, err
	}
	out.Visibility = TaskVisibility(visibility)
	return out, nil
}

// ResolveTaskInWorkspace looks up a task by public id within a known
// workspace and returns only its internal id. This is the lightweight
// lookup MCP tools use after the session has already bound the
// workspace; it does not surface visibility or creator metadata.
//
// Returns WS.TASK.NOT_FOUND when missing or when the task belongs to
// a different workspace.
func ResolveTaskInWorkspace(ctx context.Context, db DB, wsID uint32, pub uuid.UUID) (uint32, error) {
	const q = `SELECT id FROM tasks
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, wsID, types.FromUUID(pub)).Scan(&id); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.WsTaskNotFound)
		}
		return 0, err
	}
	return id, nil
}

// IsTaskActor reports whether a user has an enabled row in
// task_actors for the given task.
func IsTaskActor(ctx context.Context, db DB, taskID, userID uint32) (bool, error) {
	const q = `SELECT 1 FROM task_actors
WHERE task_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, q, taskID, userID).Scan(&one); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CheckTaskVisibility enforces Layer 4 task visibility.
//
// Visibility rules:
//   - public:  any workspace member can access (already verified upstream).
//   - project: actor must be a project member or workspace admin/owner.
//   - private: actor must be a task actor or the creator, unless they
//     hold workspace admin/owner.
//
// Returns nil when access is granted. On denial it returns
// WS.TASK.NOT_FOUND so the caller never leaks the existence of a
// private/project task they cannot see.
func CheckTaskVisibility(
	ctx context.Context,
	db DB,
	rec TaskRecord,
	userID uint32,
	wsRole WorkspaceRole,
	isProjectMember bool,
) error {
	isElevated := wsRole.AtLeast(WorkspaceRoleAdmin)
	switch rec.Visibility {
	case TaskVisibilityPublic:
		// Any workspace member -- already verified upstream.
		return nil
	case TaskVisibilityProject:
		if !isProjectMember && !isElevated {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil
	case TaskVisibilityPrivate:
		if isElevated {
			return nil
		}
		if rec.CreatedByUserID.Valid && uint32(rec.CreatedByUserID.Int32) == userID { //#nosec G115 -- created_by_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			return nil
		}
		ok, err := IsTaskActor(ctx, db, rec.ID, userID)
		if err != nil {
			return err
		}
		if !ok {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil
	default:
		// Unknown visibility values fall through to deny so adding a
		// new visibility kind without updating this switch fails closed.
		return apierrors.New(apierrors.WsTaskNotFound)
	}
}

// AuthorizeTaskAccess resolves a task by public id, verifies workspace
// membership, looks up optional project membership, and enforces task
// visibility. It is the shared Layer 3/4 decision used by REST task
// middleware and MCP task resolvers.
func AuthorizeTaskAccess(ctx context.Context, db DB, pub uuid.UUID, userID uint32) (TaskAccess, error) {
	rec, err := ResolveTaskByPublicID(ctx, db, pub)
	if err != nil {
		return TaskAccess{}, err
	}
	wsRole, err := CheckWorkspaceMember(ctx, db, rec.WorkspaceID, userID, apierrors.WsTaskAccessDenied)
	if err != nil {
		return TaskAccess{}, err
	}
	prjRole, isProjectMember, err := LookupProjectMembership(ctx, db, rec.WorkspaceID, rec.ProjectID, userID, wsRole)
	if err != nil {
		return TaskAccess{}, err
	}
	if err := CheckTaskVisibility(ctx, db, rec, userID, wsRole, isProjectMember); err != nil {
		return TaskAccess{}, err
	}
	return TaskAccess{
		Task:            rec,
		WorkspaceRole:   wsRole,
		ProjectRole:     prjRole,
		IsProjectMember: isProjectMember,
	}, nil
}

// ----------------------------------------------------------------------------
// List filters
// ----------------------------------------------------------------------------

// Layer 4 task visibility has to be expressed in two places, because
// the two kinds of query in this repository cannot share one string:
//
//   - hand-written queries splice in [TaskVisibilityFilter], a fragment
//     plus binds;
//   - sqlc queries carry the predicate in their own .sql file, because
//     sqlc parses the statement and a runtime-spliced fragment would be
//     invisible to it. Those take their binds from [ListVisibilityArgs].
//
// The rule is the same either way and neither form may be skipped. A
// list endpoint that projects a task title and applies neither is the
// recurring defect here: the shared helper existed, and its call sites
// were three, while the endpoints returning task titles were many.
// TestTaskListEndpointsHideInvisibleTitles drives the reachable ones as
// a guest and fails on any body carrying a title it should not.

// TaskVisibilityFilter returns a SQL WHERE fragment and associated bind
// arguments that enforce Layer 4 task visibility in list queries. The
// fragment references v_task_list columns and should be ANDed into an
// existing WHERE clause.
//
// The userID is the actor's internal id. wsRole is the actor's
// workspace role (from context). When the actor is a workspace admin
// or owner, no additional filtering is applied.
func TaskVisibilityFilter(userID uint32, wsRole WorkspaceRole) (fragment string, args []any) {
	if wsRole.AtLeast(WorkspaceRoleAdmin) {
		// Admins/owners see everything.
		return "", nil
	}
	const frag = `(
    v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm
      WHERE pm.project_id = v.project_id
        AND pm.user_id = ?
        AND pm.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = ?
      OR EXISTS (
        SELECT 1 FROM task_actors ta
        INNER JOIN tasks tav ON tav.id = ta.task_id AND tav.public_id = v.public_id
        WHERE ta.user_id = ?
          AND ta.enabled = TRUE
      )
    ))
  )`
	return frag, []any{userID, userID, userID}
}

// VisibilityArgs carries the bind values for the .sql-file form of the
// Layer 4 task visibility rule: the two named arguments those queries
// declare, `is_elevated` and `actor_user_id`.
//
// sqlc emits one field per textual occurrence of a named argument
// rather than deduplicating them, so a query repeating actor_user_id
// six times has six fields to fill. Callers copy ActorUserID into every
// one of them; that repetition is sqlc's, not the rule's.
type VisibilityArgs struct {
	// IsElevated is 1 for workspace admins and owners, who bypass the
	// predicate, and 0 for everyone else. An int rather than a bool
	// because MySQL has no boolean parameter type and the queries
	// CAST it to SIGNED.
	IsElevated int64
	// ActorUserID is the internal id of the user the list is being
	// rendered for — never the owner of the resource being listed.
	ActorUserID int64
}

// ListVisibilityArgs derives the binds for a sqlc list query that
// carries the visibility predicate.
//
// It exists so the elevated-role decision is made in one place. Written
// out at each call site it becomes a role comparison a reader has to
// verify against the others, and the failure mode is silent: an
// endpoint that computes IsElevated too generously shows every task to
// everyone, and nothing about the query looks wrong.
func ListVisibilityArgs(actorID uint32, wsRole WorkspaceRole) VisibilityArgs {
	var elevated int64
	if wsRole.AtLeast(WorkspaceRoleAdmin) {
		elevated = 1
	}
	return VisibilityArgs{IsElevated: elevated, ActorUserID: int64(actorID)}
}

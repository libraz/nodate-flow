// Package mcp ACL helpers. The actual access decisions live in
// apps/flow-api/internal/acl so they cannot drift between the HTTP
// middleware and the MCP transport. This file is the thin MCP-side
// wrapper that handles bearer-token / session extraction and resource
// resolution that is specific to MCP (calendar, page).
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// session is the per-request MCP caller context. It carries the
// resolved internal user id, the workspace the token is bound to,
// and the parsed scopes list.
type session struct {
	userID      uint32
	workspaceID uint32
	// agentID is the internal ai_agents.id when this MCP token acts on
	// behalf of an AI agent. Zero means a human-owned token.
	agentID uint32
	scopes  []string
}

// hasScope reports whether the session's granted scopes satisfy the
// tool's required scope.
//
// The vocabulary is the closed set in [SupportedScopes]:
//
//   - read:workspace  — invoke read-only tools across the workspace.
//   - write:workspace — additionally invoke every mutating tool; it
//     widens to read:workspace.
//
// Matching is therefore a membership test with write-implies-read
// widening — there is no "read:calendar" or "write:task:complete"
// granularity. A token holding write:workspace can invoke any mutating
// tool; the resource it touches is still constrained, but by the
// workspace-scoped resolvers in this file (resolveTask / resolveCalendar /
// resolveWorkspaceUser, …) and the agent guard, not by the scope string.
// This keeps the scope surface small and auditable; per-resource least
// privilege is a deliberate future extension, not an isolation gap (the
// resolvers, not the scopes, enforce tenancy).
func (s *session) hasScope(required string) bool {
	if required == "" {
		return true
	}
	for _, sc := range s.scopes {
		if sc == required {
			return true
		}
		// write:workspace widens to read:workspace.
		if required == ScopeReadWorkspace && sc == ScopeWriteWorkspace {
			return true
		}
	}
	return false
}

// authenticate resolves the MCP bearer token into a session. It checks
// token revocation, expiry, parses scopes_json, and returns the
// resolved workspace and user ids. Scope enforcement is applied per
// tool call, not here.
func (h *Handler) authenticate(ctx context.Context, tok string) (*session, error) {
	if h.deps.Queries == nil {
		return nil, apierrors.New(apierrors.InternalUnexpected)
	}
	row, err := h.deps.Queries.FindUserForMcpToken(ctx, auth.HashOpaque(tok))
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.McpTokenUnknown)
		}
		return nil, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return nil, apierrors.New(apierrors.McpTokenExpired)
	}
	scopes := parseScopes(row.ScopesJson)
	var agentID uint32
	if row.AgentID.Valid {
		agentID = uint32(row.AgentID.Int32) //#nosec G115 -- agent_id is agents.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	}
	// Stamp last_used_at so the token-list UI can surface usage and a
	// leaked-token signal is not dead. Best-effort only: auth must never
	// fail on the stamp, so it runs fire-and-forget on a detached context.
	h.touchTokenLastUsed(ctx, row.TokenID)
	return &session{
		userID:      row.UserID,
		workspaceID: row.WorkspaceID,
		agentID:     agentID,
		scopes:      scopes,
	}, nil
}

// touchTokenLastUsed records that an MCP token was just used for a
// successful authentication. It is intentionally non-blocking and
// error-swallowing: the request must not wait on, or fail because of, the
// usage stamp. The context is detached from the request so the update can
// complete even after the caller's context is cancelled.
func (h *Handler) touchTokenLastUsed(ctx context.Context, tokenID uint32) {
	q := h.deps.Queries
	if q == nil {
		return
	}
	go func() {
		stampCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = q.TouchMcpTokenLastUsed(stampCtx, tokenID)
	}()
}

// parseScopes tolerantly decodes the scopes_json column. The column is
// expected to hold a JSON array of strings; whitespace-separated fallback
// is allowed for future compatibility.
func parseScopes(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	// Try JSON array first.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	// Fall back to whitespace-separated string.
	return strings.Fields(strings.Trim(string(raw), `"`))
}

// requireWorkspaceMember verifies the acting user is an enabled member
// of the session workspace. Returns the workspace role on success.
//
// Delegates to the shared [acl.CheckWorkspaceMember] so the rule
// cannot drift from the HTTP middleware's behavior.
func requireWorkspaceMember(ctx context.Context, deps Deps, s *session) (acl.WorkspaceRole, error) {
	role, err := acl.CheckWorkspaceMember(ctx, deps.DB, s.workspaceID, s.userID, nil)
	if err != nil {
		return acl.WorkspaceRole(""), err
	}
	return role, nil
}

// resolveProject resolves a project public id to its internal id and
// verifies it belongs to the session workspace. Returns the internal
// project id.
//
// This is the *read* gate: it proves tenancy but says nothing about the
// caller's project role. Anything that writes inside the project must use
// [resolveProjectForWrite] instead.
//
// Delegates to [acl.ResolveProjectByPublicID] for the lookup and
// applies the workspace-binding check that is specific to MCP tokens.
func resolveProject(ctx context.Context, deps Deps, s *session, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, apierrors.New(apierrors.WsProjectNotFound)
	}
	prj, err := acl.ResolveProjectByPublicID(ctx, deps.DB, pub.UUID())
	if err != nil {
		return 0, err
	}
	if prj.WorkspaceID != s.workspaceID {
		return 0, apierrors.New(apierrors.McpTokenWorkspaceMismatch)
	}
	return prj.ID, nil
}

// checkProjectRoleFloor applies the Layer-3 project-role floor to an already
// resolved role. It is the MCP mirror of REST RequireProjectRole
// (apps/flow-api/internal/http/middleware/acl.go): workspace owners / admins
// arrive as [acl.ProjectRoleElevated] and pass unconditionally, everyone else
// must meet minRole, and denial is WS.PROJECT.ACCESS_DENIED — the same code
// the HTTP middleware writes.
func checkProjectRoleFloor(role, minRole acl.ProjectRole) error {
	if role == acl.ProjectRoleElevated || role.AtLeast(minRole) {
		return nil
	}
	return apierrors.New(apierrors.WsProjectAccessDenied)
}

// resolveProjectForWrite resolves a project public id for a tool that writes
// inside that project. On top of [resolveProject]'s tenancy check it enforces
// the Layer-3 project-role floor, so a workspace member with no
// project_members row cannot create rows in a project they were never added
// to.
//
// The decision is the same chain REST runs for the project-scoped writes that
// have no path parameter to hang RequireProjectRole on
// (tasks.requireProjectEditor / intake.requireProjectEditor):
// acl.CheckWorkspaceMember → acl.LookupProjectMembership → role floor.
func resolveProjectForWrite(ctx context.Context, deps Deps, s *session, publicID string, minRole acl.ProjectRole) (uint32, error) {
	prjID, err := resolveProject(ctx, deps, s, publicID)
	if err != nil {
		return 0, err
	}
	wsRole, err := acl.CheckWorkspaceMember(ctx, deps.DB, s.workspaceID, s.userID, apierrors.WsProjectAccessDenied)
	if err != nil {
		return 0, err
	}
	role, _, err := acl.LookupProjectMembership(ctx, deps.DB, s.workspaceID, prjID, s.userID, wsRole)
	if err != nil {
		return 0, err
	}
	if err := checkProjectRoleFloor(role, minRole); err != nil {
		return 0, err
	}
	return prjID, nil
}

// authorizeTask runs the shared Layer-3/4 task ACL and the MCP-specific
// workspace binding, returning the full access result so callers can apply a
// role floor on top. Every MCP task resolver funnels through here.
func authorizeTask(ctx context.Context, deps Deps, s *session, publicID string) (acl.TaskAccess, types.PublicID, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return acl.TaskAccess{}, types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	access, err := acl.AuthorizeTaskAccess(ctx, deps.DB, pub.UUID(), s.userID)
	if err != nil {
		return acl.TaskAccess{}, types.PublicID{}, err
	}
	if access.Task.WorkspaceID != s.workspaceID {
		return acl.TaskAccess{}, types.PublicID{}, apierrors.New(apierrors.McpTokenWorkspaceMismatch)
	}
	return access, pub, nil
}

// resolveTask resolves a task public id, verifies it belongs to the session
// workspace, and enforces the same project-membership / task-visibility
// decision used by REST RequireTaskAccess.
//
// This is the *read* gate. Being able to see a task is not permission to
// change it, so mutating tools must use [resolveTaskForWrite] /
// [resolveTaskRowForWrite], which additionally apply the project-role floor
// REST attaches with RequireProjectRole.
func resolveTask(ctx context.Context, deps Deps, s *session, publicID string) (uint32, types.PublicID, error) {
	access, pub, err := authorizeTask(ctx, deps, s, publicID)
	if err != nil {
		return 0, types.PublicID{}, err
	}
	return access.Task.ID, pub, nil
}

// resolveTaskForWrite is [resolveTask] plus the Layer-3 project-role floor.
// It is the MCP equivalent of the REST chain RequireTaskAccess +
// RequireProjectRole(minRole), reusing the very same role that
// acl.AuthorizeTaskAccess hands the HTTP middleware, so a project viewer (or
// a workspace member with no project_members row at all) who can read a task
// cannot mutate it.
func resolveTaskForWrite(ctx context.Context, deps Deps, s *session, publicID string, minRole acl.ProjectRole) (uint32, types.PublicID, error) {
	access, pub, err := authorizeTask(ctx, deps, s, publicID)
	if err != nil {
		return 0, types.PublicID{}, err
	}
	if err := checkProjectRoleFloor(access.ProjectRole, minRole); err != nil {
		return 0, types.PublicID{}, err
	}
	return access.Task.ID, pub, nil
}

// loadTaskRow is the single MCP entry point to the tasks public-id lookup.
// Every FindTaskByPublicId call in this package routes through here so a
// static guard (acl_static_test.go) can prove no tool reads task rows
// without first authorizing access via resolveTask / resolveTaskRow. The
// raw sql.ErrNoRows is preserved so callers that only probe existence
// (e.g. favorites) keep their own not-found handling.
func loadTaskRow(ctx context.Context, q *generated.Queries, workspaceID uint32, pub types.PublicID) (generated.FindTaskByPublicIdRow, error) {
	return q.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: workspaceID,
		PublicID:    pub,
	})
}

// resolveTaskRow authorizes access to a task through the shared
// task-visibility ACL (resolveTask) and then loads its full row. Tools
// that need task fields must use this instead of loading the row directly,
// so a caller can never read task data it is not permitted to see. Missing
// and access-denied both surface as WS.TASK.NOT_FOUND, so the tool is not
// an existence oracle.
func resolveTaskRow(ctx context.Context, deps Deps, s *session, publicID string) (uint32, generated.FindTaskByPublicIdRow, error) {
	internalID, pub, err := resolveTask(ctx, deps, s, publicID)
	if err != nil {
		return 0, generated.FindTaskByPublicIdRow{}, err
	}
	return fetchTaskRow(ctx, deps, s, internalID, pub)
}

// resolveTaskRowForWrite is [resolveTaskRow] with the Layer-3 project-role
// floor applied, for mutating tools that also need the current task fields.
func resolveTaskRowForWrite(ctx context.Context, deps Deps, s *session, publicID string, minRole acl.ProjectRole) (uint32, generated.FindTaskByPublicIdRow, error) {
	internalID, pub, err := resolveTaskForWrite(ctx, deps, s, publicID, minRole)
	if err != nil {
		return 0, generated.FindTaskByPublicIdRow{}, err
	}
	return fetchTaskRow(ctx, deps, s, internalID, pub)
}

// fetchTaskRow loads a task row that the caller has already been authorized
// for. Missing rows surface as WS.TASK.NOT_FOUND so a row that vanished
// between the ACL decision and the load is not an execution failure.
func fetchTaskRow(ctx context.Context, deps Deps, s *session, internalID uint32, pub types.PublicID) (uint32, generated.FindTaskByPublicIdRow, error) {
	row, err := loadTaskRow(ctx, deps.Queries, s.workspaceID, pub)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, generated.FindTaskByPublicIdRow{}, apierrors.New(apierrors.WsTaskNotFound)
		}
		return 0, generated.FindTaskByPublicIdRow{}, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return internalID, row, nil
}

// resolvePage resolves a page public id to its internal id and verifies
// it belongs to the session workspace.
func resolvePage(ctx context.Context, deps Deps, s *session, publicID string) (uint32, types.PublicID, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, types.PublicID{}, apierrors.New(apierrors.PagePageNotFound)
	}
	row, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, types.PublicID{}, apierrors.New(apierrors.PagePageNotFound)
		}
		return 0, types.PublicID{}, err
	}
	return row.ID, pub, nil
}

// resolveCalendar resolves a calendar public id to its internal id and
// verifies it belongs to the session workspace.
func resolveCalendar(ctx context.Context, deps Deps, s *session, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid calendar id")
	}
	row, err := deps.CalendarQueries.FindCalendarByPublicId(ctx, calendar.FindCalendarByPublicIdParams{
		PublicID:    pub,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.Newf(apierrors.McpToolExecutionFailed, "calendar not found")
		}
		return 0, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Access is calendar_members, not the subscription: a subscription is a
	// display preference and grants nothing.
	if _, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
		CalendarID: row.ID,
		UserID:     s.userID,
	}); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.CalendarCalendarAccessDenied)
		}
		return 0, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return row.ID, nil
}

// resolveWorkspaceUser resolves a user public id to its internal id and
// verifies the user is an enabled member of the session workspace.
//
// This mirrors [acl.CheckWorkspaceMember]'s membership rule but binds it
// to a public-id lookup so a caller cannot assign a globally-existing
// user that is not a member of their workspace (the FK on
// calendar_events.owner_user_id references the global users table, so a
// non-member would otherwise pass referential integrity). Non-members
// surface as MCP.TOKEN.WORKSPACE_MISMATCH, never leaking whether the
// user exists outside the workspace.
func resolveWorkspaceUser(ctx context.Context, deps Deps, s *session, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid userId")
	}
	const q = `SELECT u.id FROM users u
INNER JOIN workspace_members wm
  ON wm.user_id = u.id
  AND wm.workspace_id = ?
  AND wm.enabled = TRUE
WHERE u.public_id = ? AND u.enabled = TRUE
LIMIT 1`
	var userID uint32
	if err := deps.DB.QueryRowContext(ctx, q, s.workspaceID, pub).Scan(&userID); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.McpTokenWorkspaceMismatch)
		}
		return 0, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return userID, nil
}

// requireCalendarMembership refuses a caller who holds no
// calendar_members row on the given calendar.
//
// The REST handlers reach every event through resolveCalendar, so
// membership is checked before the edit rule is ever consulted. The MCP
// event tools resolve the event by its own public id and went straight
// to the edit rule, which let somebody removed from a calendar keep
// editing the events on it that they happen to own. Membership is the
// outer gate on both transports now.
func requireCalendarMembership(ctx context.Context, deps Deps, s *session, calendarID uint32) error {
	if _, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
		CalendarID: calendarID,
		UserID:     s.userID,
	}); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.New(apierrors.CalendarCalendarAccessDenied)
		}
		return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return nil
}

// canEditCalendarEvent answers the same question the REST handlers ask,
// through the same rule: eventacl.CanEdit.
//
// It used to have its own: event owner, can_edit attendee, or the holder
// of calendars.owner_user_id. That third clause is why an agent refused
// edits the web app allowed. A shared calendar leaves owner_user_id NULL
// by design, so on exactly the calendars that have managers, no manager
// qualified — and on a personal calendar the clause was redundant, since
// its owner also holds the owner row in calendar_members.
func canEditCalendarEvent(ctx context.Context, deps Deps, s *session, eventOwnerUserID uint32, eventID uint32, calendarID uint32) (bool, error) {
	actor := eventacl.Editor{UserID: s.userID}

	if att, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
		EventID: sql.NullInt32{Int32: int32(eventID), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic deployments
		UserID:  s.userID,
	}); err == nil {
		actor.AttendeeCanEdit = att.CanEdit
	} else if !stderrors.Is(err, sql.ErrNoRows) {
		return false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if member, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
		CalendarID: calendarID,
		UserID:     s.userID,
	}); err == nil {
		actor.CalendarRole = eventacl.Role(member.Role)
	} else if !stderrors.Is(err, sql.ErrNoRows) {
		return false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	return eventacl.CanEdit(eventOwnerUserID, actor), nil
}

func newPublicID() types.PublicID { return types.New() }

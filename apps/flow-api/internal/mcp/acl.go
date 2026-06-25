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

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/acl"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
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
// Scopes are coarse access *tiers*, not per-tool or per-resource
// capabilities. The vocabulary is fixed and two-tiered:
//
//   - read:workspace  — invoke read-only tools across the workspace.
//   - write:workspace — additionally invoke every mutating tool; it
//     widens to read:workspace and to both project-tier scopes.
//   - read:project / write:project — the same two tiers narrowed to a
//     project; write:project widens to read:project, and write:workspace
//     widens to both.
//
// Matching is therefore a membership test with write-implies-read (and
// workspace-implies-project) widening — there is no "read:calendar" or
// "write:task:complete" granularity. A token holding write:workspace can
// invoke any mutating tool; the resource it touches is still constrained,
// but by the workspace-scoped resolvers in this file (resolveTask /
// resolveCalendar / resolveWorkspaceUser, …) and the agent guard, not by
// the scope string. This keeps the scope surface small and auditable;
// per-resource least privilege is a deliberate future extension, not an
// isolation gap (the resolvers, not the scopes, enforce tenancy).
func (s *session) hasScope(required string) bool {
	if required == "" {
		return true
	}
	for _, sc := range s.scopes {
		if sc == required {
			return true
		}
		// write:* implies read:* at the same resource tier.
		if required == "read:workspace" && sc == "write:workspace" {
			return true
		}
		if required == "read:project" && (sc == "write:project" || sc == "write:workspace") {
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
	return &session{
		userID:      row.UserID,
		workspaceID: row.WorkspaceID,
		agentID:     agentID,
		scopes:      scopes,
	}, nil
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
func requireWorkspaceMember(ctx context.Context, deps Deps, s *session) (string, error) {
	role, err := acl.CheckWorkspaceMember(ctx, deps.DB, s.workspaceID, s.userID, nil)
	if err != nil {
		return "", err
	}
	return string(role), nil
}

// resolveProject resolves a project public id to its internal id and
// verifies it belongs to the session workspace. Returns the internal
// project id.
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

// resolveTask resolves a task public id to its internal id and verifies
// it belongs to the session workspace.
//
// Delegates to [acl.ResolveTaskInWorkspace] which performs the bounded
// "by workspace + public id" lookup the MCP transport expects.
func resolveTask(ctx context.Context, deps Deps, s *session, publicID string) (uint32, types.PublicID, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	id, err := acl.ResolveTaskInWorkspace(ctx, deps.DB, s.workspaceID, pub.UUID())
	if err != nil {
		return 0, types.PublicID{}, err
	}
	return id, pub, nil
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

// canEditCalendarEvent checks if the acting user can edit a calendar
// event. Returns true if the user is the event owner, a can_edit
// attendee, or the personal-calendar owner.
//
// calendar_subscriptions.role has been dropped, so there is no
// per-subscription manager/owner tier anymore. The calendar-level
// "owner" is whoever holds calendars.owner_user_id (personal layer).
// System calendars (kind=system) are read-only and have no editable
// owner. Attendee can_edit is still honored via FindCalendarEventAttendee.
func canEditCalendarEvent(ctx context.Context, deps Deps, s *session, eventOwnerUserID uint32, eventID uint32, calendarID uint32) (bool, error) {
	if s.userID == eventOwnerUserID {
		return true, nil
	}
	att, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
		EventID: sql.NullInt32{Int32: int32(eventID), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic deployments
		UserID:  s.userID,
	})
	if err == nil && att.CanEdit {
		return true, nil
	}
	// Look up the personal-calendar owner via raw SQL. The sqlc
	// FindCalendarByPublicId query needs the public id, which we do
	// not have here; a lightweight single-column lookup by internal
	// id is appropriate and matches the pattern used elsewhere in
	// this package.
	const q = `SELECT owner_user_id FROM calendars WHERE id = ? AND enabled = TRUE LIMIT 1`
	var ownerID sql.NullInt32
	if err := deps.DB.QueryRowContext(ctx, q, calendarID).Scan(&ownerID); err != nil {
		return false, nil
	}
	if ownerID.Valid && uint32(ownerID.Int32) == s.userID { //#nosec G115 -- owner_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		return true, nil
	}
	return false, nil
}

func newPublicID() types.PublicID { return types.New() }

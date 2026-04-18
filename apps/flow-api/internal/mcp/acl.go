// Package mcp ACL helpers. These are intentionally duplicated from
// internal/http/middleware/acl.go (Path A) rather than refactored into
// a shared package, to avoid churn in the REST handlers. The lookups
// are small (workspace_members / project_members) and stable.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
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
		agentID = uint32(row.AgentID.Int32)
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
func requireWorkspaceMember(ctx context.Context, deps Deps, s *session) (string, error) {
	row, err := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
		WorkspaceID: s.workspaceID,
		UserID:      s.userID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return "", apierrors.New(apierrors.WsWorkspaceAccessDenied)
		}
		return "", err
	}
	return string(row.Role), nil
}

// resolveProject resolves a project public id to its internal id and
// verifies it belongs to the session workspace. Returns the internal
// project id.
func resolveProject(ctx context.Context, deps Deps, s *session, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, apierrors.New(apierrors.WsProjectNotFound)
	}
	row, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, pub)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, apierrors.New(apierrors.WsProjectNotFound)
		}
		return 0, err
	}
	if row.WorkspaceID != s.workspaceID {
		return 0, apierrors.New(apierrors.McpTokenWorkspaceMismatch)
	}
	return row.ID, nil
}

// resolveTask resolves a task public id to its internal id and verifies
// it belongs to the session workspace.
func resolveTask(ctx context.Context, deps Deps, s *session, publicID string) (uint32, types.PublicID, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := deps.DB.QueryRowContext(ctx, q, s.workspaceID, pub).Scan(&id); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, types.PublicID{}, apierrors.New(apierrors.WsTaskNotFound)
		}
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
	const q = `SELECT id FROM pages WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := deps.DB.QueryRowContext(ctx, q, s.workspaceID, pub).Scan(&id); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return 0, types.PublicID{}, apierrors.New(apierrors.PagePageNotFound)
		}
		return 0, types.PublicID{}, err
	}
	return id, pub, nil
}

// resolveCalendar resolves a calendar public id to its internal id and
// verifies it belongs to the session workspace.
func resolveCalendar(ctx context.Context, deps Deps, s *session, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid calendar id")
	}
	row, err := deps.Queries.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
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

// canEditCalendarEvent checks if the acting user can edit a calendar
// event. Returns true if the user is the event owner, a can_edit
// attendee, or a manager/owner on the calendar subscription.
func canEditCalendarEvent(ctx context.Context, deps Deps, s *session, eventOwnerUserID uint32, eventID uint32, calendarID uint32) (bool, error) {
	if s.userID == eventOwnerUserID {
		return true, nil
	}
	att, err := deps.Queries.FindCalendarEventAttendee(ctx, generated.FindCalendarEventAttendeeParams{
		EventID: eventID,
		UserID:  s.userID,
	})
	if err == nil && att.CanEdit {
		return true, nil
	}
	sub, err := deps.Queries.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
		CalendarID: calendarID,
		UserID:     s.userID,
	})
	if err != nil {
		return false, nil
	}
	role := string(sub.Role)
	return role == "manager" || role == "owner", nil
}

func newPublicID() types.PublicID { return types.New() }

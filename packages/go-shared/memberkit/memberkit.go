// Package memberkit is the sole writer for cross-table mutations that
// span workspace_members and the workspace-scoped state owned by each
// member (personal calendars, calendar_subscriptions, task_actors,
// project_members). Every function runs inside a caller-provided
// *sql.Tx so membership and the downstream rows move in lockstep.
//
// Design principle: joining a workspace and leaving a workspace are
// not single-row updates. Join must materialise the user's personal
// calendar layer so the calendar page has something to show on first
// visit. Leave must soft-disable every workspace-scoped row the user
// owns so a revoked member cannot keep seeing subscribed calendars
// via caches or misconfigured middleware.
//
// memberkit does NOT enforce ACL — callers (auth-api workspace
// handlers) authorise the actor before invoking. memberkit writes
// only.
//
// Raw SQL is used rather than sqlc because this package is imported
// by auth-api and (indirectly, via tests) flow-api — sqlc is generated
// per-service and can't produce a shared queries type. The flow-api
// internal itemkit follows the same raw-SQL pattern within its own
// service for cross-table writes.
package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// Sentinel errors returned by the membership-mutation guards. Callers
// (auth-api / flow-api workspace handlers) map these to the
// WS.MEMBER.SELF_MODIFY (403) and WS.MEMBER.LAST_OWNER (409) API error
// specs via errors.Is. They are defined here — rather than as service
// apierr specs — because memberkit is the shared writer imported by
// every service and must not depend on a service's errors package.
var (
	// ErrSelfModify is returned when an actor attempts to change their
	// own membership role or remove themselves from the workspace.
	ErrSelfModify = errors.New("memberkit: actor cannot modify own membership")

	// ErrLastOwner is returned when an operation would demote or remove
	// the last remaining owner of the workspace. At least one owner
	// must always remain.
	ErrLastOwner = errors.New("memberkit: workspace must keep at least one owner")
)

// countWorkspaceOwners returns the number of enabled members holding
// the owner role in the workspace. Used by the last-owner guard so a
// demote/remove never drops the owner count to zero.
//
// owner is the workspace top role in the workspace_members.role enum
// (owner > admin > member > guest); it is distinct from the project
// member role hierarchy.
func countWorkspaceOwners(ctx context.Context, tx TX, wsID uint32) (int, error) {
	const q = `SELECT COUNT(*) FROM workspace_members
	           WHERE workspace_id = ? AND role = ? AND enabled = TRUE`
	var n int
	if err := tx.QueryRowContext(ctx, q, wsID, string(RoleOwner)).Scan(&n); err != nil {
		return 0, fmt.Errorf("memberkit: count owners: %w", err)
	}
	return n, nil
}

// Role is the enum value stored in workspace_members.role. The string
// values must match the SQL ENUM exactly.
type Role string

// Known workspace roles. owner/admin/member/guest mirror the schema.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

// IsValid reports whether the role is one of the four allowed values.
func (r Role) IsValid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember, RoleGuest:
		return true
	}
	return false
}

// TX is the minimal transaction surface memberkit needs. *sql.Tx
// satisfies it. Passing *sql.DB would split the member insert and the
// calendar materialisation across connections and defeat the
// atomicity guarantee; memberkit therefore rejects it at the type
// level.
type TX interface {
	ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row
	QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error)
}

// workspaceRow is memberkit's minimal projection of a workspaces row —
// just the fields needed to decide whether to auto-subscribe to a
// system (holiday) calendar.
type workspaceRow struct {
	id      uint32
	country sql.NullString
}

// findWorkspaceByID reads the workspace's country for holiday
// subscription. Returns sql.ErrNoRows when the workspace is missing
// or disabled.
func findWorkspaceByID(ctx context.Context, tx TX, wsID uint32) (workspaceRow, error) {
	const q = `SELECT id, country FROM workspaces WHERE id = ? AND enabled = TRUE`
	var w workspaceRow
	err := tx.QueryRowContext(ctx, q, wsID).Scan(&w.id, &w.country)
	return w, err
}

// findExistingMember returns (memberID, enabled, nil) when the row
// exists, (0, false, sql.ErrNoRows) when it does not.
func findExistingMember(ctx context.Context, tx TX, wsID, userID uint32) (uint32, bool, error) {
	const q = `SELECT id, enabled FROM workspace_members
	           WHERE workspace_id = ? AND user_id = ?
	           LIMIT 1`
	var id uint32
	var enabled bool
	err := tx.QueryRowContext(ctx, q, wsID, userID).Scan(&id, &enabled)
	if err != nil {
		return 0, false, err
	}
	return id, enabled, nil
}

// findUserDisplayName returns users.display_name for use as the
// personal calendar name. Falls back to "My Calendar" when the user
// row is missing a display name or doesn't exist.
func findUserDisplayName(ctx context.Context, tx TX, userID uint32) string {
	const q = `SELECT display_name FROM users WHERE id = ? LIMIT 1`
	var name string
	if err := tx.QueryRowContext(ctx, q, userID).Scan(&name); err != nil || name == "" {
		return "My Calendar"
	}
	return name
}

// findPersonalCalendar returns the ID of the user's existing personal
// calendar in the workspace, or sql.ErrNoRows when none exists.
// Multiple personal calendars per user are allowed by the schema,
// so this returns the first one the schema chooses (lowest id).
func findPersonalCalendar(ctx context.Context, tx TX, wsID, userID uint32) (uint32, error) {
	const q = `SELECT id FROM calendars
	           WHERE workspace_id = ?
	             AND kind = 'personal'
	             AND owner_user_id = ?
	             AND enabled = TRUE
	           ORDER BY id
	           LIMIT 1`
	var id uint32
	err := tx.QueryRowContext(ctx, q, wsID, userID).Scan(&id)
	return id, err
}

// findSystemHolidayCalendar returns the ID of the workspace's system
// holiday calendar for the given country, or sql.ErrNoRows when none
// exists yet.
func findSystemHolidayCalendar(ctx context.Context, tx TX, wsID uint32, country string) (uint32, error) {
	const q = `SELECT id FROM calendars
	           WHERE workspace_id = ?
	             AND kind = 'system'
	             AND system_slug = ?
	           LIMIT 1`
	var id uint32
	err := tx.QueryRowContext(ctx, q, wsID, holidaySlug(country)).Scan(&id)
	return id, err
}

// holidaySlug builds the canonical system_slug for a country's
// holiday feed. Mirrors flow-api calendars/auto_create.holidaySlug.
func holidaySlug(country string) string {
	return "holidays." + lowercase(country)
}

// lowercase is a tiny helper so memberkit does not pull in strings
// just for ToLower.
func lowercase(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// createCalendar inserts a calendars row and returns its internal id.
// kind must be 'personal' or 'system'. For 'personal' the caller must
// pass a non-zero ownerUserID; for 'system' ownerUserID must be 0 and
// systemSlug must be non-empty.
func createCalendar(ctx context.Context, tx TX, wsID uint32, kind, name, color string,
	ownerUserID uint32, systemSlug string,
) (uint32, error) {
	publicID := dbtype.New()
	var ownerArg any
	if ownerUserID != 0 {
		ownerArg = ownerUserID
	}
	var slugArg any
	if systemSlug != "" {
		slugArg = systemSlug
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO calendars
		 (public_id, workspace_id, kind, name, color, owner_user_id, system_slug)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		publicID[:], wsID, kind, name, color, ownerArg, slugArg)
	if err != nil {
		return 0, fmt.Errorf("memberkit: create %s calendar: %w", kind, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("memberkit: LastInsertId: %w", err)
	}
	return uint32(id), nil
}

// createSubscription inserts (or re-enables) a calendar_subscriptions
// row. It is idempotent on (calendar_id, user_id): a duplicate insert
// is converted into a re-enable.
func createSubscription(ctx context.Context, tx TX, wsID, calendarID, userID uint32, color string) error {
	publicID := dbtype.New()
	_, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_subscriptions
		 (public_id, workspace_id, calendar_id, user_id, display_color)
		 VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = TRUE, display_color = VALUES(display_color)`,
		publicID[:], wsID, calendarID, userID, color)
	if err != nil {
		return fmt.Errorf("memberkit: create subscription: %w", err)
	}
	return nil
}

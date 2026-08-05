package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// Relation mirrors the task_event_links.relation enum. itemkit validates
// this at the boundary so unknown strings do not reach the DB driver.
type Relation string

const (
	// RelationContributesTo — the task is work toward an umbrella event.
	RelationContributesTo Relation = "contributes_to"
	// RelationBlocks — the task blocks the event from happening.
	RelationBlocks Relation = "blocks"
	// RelationDependsOn — the task depends on the event.
	RelationDependsOn Relation = "depends_on"
	// RelationPrepFor — the task is prep work before the event.
	RelationPrepFor Relation = "prep_for"
)

// IsValid reports whether r is one of the four supported relations.
func (r Relation) IsValid() bool {
	switch r {
	case RelationContributesTo, RelationBlocks, RelationDependsOn, RelationPrepFor:
		return true
	}
	return false
}

// LinkTaskToEventArgs carries the inputs for a single link insert.
// Actor authorization (task edit + event read) is a handler concern —
// itemkit only validates that both sides exist and are in the same
// workspace.
type LinkTaskToEventArgs struct {
	WorkspaceID uint32
	TaskID      uint32
	EventID     uint32
	Relation    Relation
	ActorUserID uint32
	// SortWeight is the display order hint. Zero is acceptable; larger
	// numbers sink lower.
	SortWeight int32
}

// LinkTaskToEvent creates a task_event_links row or returns the
// existing enabled link when (task, event, relation) already exists.
// Returns the link's public_id and internal id. Callers should surface
// a 409 Conflict only when they care to distinguish create-vs-return;
// the default contract is idempotent.
func LinkTaskToEvent(ctx context.Context, tx TX, args LinkTaskToEventArgs) (dbtype.PublicID, uint32, error) {
	if args.WorkspaceID == 0 || args.TaskID == 0 || args.EventID == 0 {
		return dbtype.PublicID{}, 0, wrapInvariant("link_ids_required", "workspaceID, taskID and eventID must be non-zero")
	}
	if !args.Relation.IsValid() {
		return dbtype.PublicID{}, 0, wrapInvariant("link_relation_valid", fmt.Sprintf("unknown relation %q", args.Relation))
	}

	// Both sides must exist, be enabled, and live in the same workspace.
	if _, err := findTaskByID(ctx, tx, args.WorkspaceID, args.TaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: task %d not found in ws %d", args.TaskID, args.WorkspaceID)
		}
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: read task: %w", err)
	}
	if _, err := findEventByID(ctx, tx, args.WorkspaceID, args.EventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: event %d not found in ws %d", args.EventID, args.WorkspaceID)
		}
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: read event: %w", err)
	}

	// Idempotency: if an enabled (task, event, relation) row already
	// exists, return it without appending a duplicate event.
	existingID, existingPub, err := findActiveLink(ctx, tx, args)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: find active link: %w", err)
	}
	if err == nil {
		return existingPub, existingID, nil
	}

	pub := dbtype.New()
	const insertSQL = `INSERT INTO task_event_links
	    (public_id, workspace_id, task_id, event_id, relation, sort_weight)
	    VALUES (?, ?, ?, ?, ?, ?)`
	res, err := tx.ExecContext(ctx, insertSQL,
		pub, args.WorkspaceID, args.TaskID, args.EventID,
		string(args.Relation), args.SortWeight,
	)
	if err != nil {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: insert task_event_links: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: last insert id: %w", err)
	}
	linkID := uint32(id64) //#nosec G115 -- LastInsertId for task_event_links.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

	payload := map[string]any{
		"linkPublicId": pub.String(),
		"taskId":       args.TaskID,
		"eventId":      args.EventID,
		"relation":     string(args.Relation),
	}
	actor := args.ActorUserID
	taskPtr := &args.TaskID
	if err := appendItemEvents(ctx, tx, eventbus.ItemMilestoneLinkAdded, args.WorkspaceID, &actor, taskPtr, payload); err != nil {
		return dbtype.PublicID{}, 0, err
	}
	return pub, linkID, nil
}

// UnlinkTaskFromEventArgs carries the inputs for a single link removal.
type UnlinkTaskFromEventArgs struct {
	WorkspaceID uint32
	LinkID      dbtype.PublicID
	ActorUserID uint32
}

// UnlinkTaskFromEvent soft-disables a link row by its public ID.
// Returns sql.ErrNoRows when the link does not exist or is already
// soft-disabled in the given workspace.
func UnlinkTaskFromEvent(ctx context.Context, tx TX, args UnlinkTaskFromEventArgs) error {
	if args.WorkspaceID == 0 {
		return wrapInvariant("link_workspace_required", "workspaceID must be non-zero")
	}

	// Load the row first so we can emit a useful audit event.
	var (
		taskID, eventID uint32
		relation        string
	)
	err := tx.QueryRowContext(ctx,
		`SELECT task_id, event_id, relation
		 FROM task_event_links
		 WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
		 LIMIT 1`,
		args.WorkspaceID, args.LinkID,
	).Scan(&taskID, &eventID, &relation)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("itemkit: load link: %w", err)
	}

	const updSQL = `UPDATE task_event_links
	                SET enabled = FALSE
	                WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE`
	if _, err := tx.ExecContext(ctx, updSQL, args.WorkspaceID, args.LinkID); err != nil {
		return fmt.Errorf("itemkit: soft-disable link: %w", err)
	}

	payload := map[string]any{
		"linkPublicId": args.LinkID.String(),
		"taskId":       taskID,
		"eventId":      eventID,
		"relation":     relation,
	}
	actor := args.ActorUserID
	t := taskID
	return appendItemEvents(ctx, tx, eventbus.ItemMilestoneLinkRemoved, args.WorkspaceID, &actor, &t, payload)
}

// findActiveLink returns the (id, public_id) of an enabled link for
// (task, event, relation) in the workspace, or sql.ErrNoRows when
// none exists.
func findActiveLink(ctx context.Context, tx TX, args LinkTaskToEventArgs) (uint32, dbtype.PublicID, error) {
	const q = `SELECT id, public_id
	           FROM task_event_links
	           WHERE workspace_id = ? AND task_id = ? AND event_id = ?
	             AND relation = ? AND enabled = TRUE
	           LIMIT 1`
	var (
		id  uint32
		pub dbtype.PublicID
	)
	err := tx.QueryRowContext(ctx, q,
		args.WorkspaceID, args.TaskID, args.EventID, string(args.Relation),
	).Scan(&id, &pub)
	return id, pub, err
}

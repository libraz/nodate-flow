package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

const seedProjectSlug = "demo-project"

func ensureProject(ctx context.Context, db *sql.DB, q *generated.Queries, wsID uint32, l seedLocale) (int64, bool, error) {
	var id uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM projects WHERE workspace_id = ? AND slug = ?",
		wsID, seedProjectSlug,
	).Scan(&id)
	if err == nil {
		return int64(id), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	newID, err := q.CreateProject(ctx, generated.CreateProjectParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		Slug:        seedProjectSlug,
		Name:        l.ProjectName,
		Description: sql.NullString{String: l.ProjectDesc, Valid: true},
	})
	if err != nil {
		return 0, false, err
	}
	return newID, true, nil
}

// ensureTasks returns the internal id of the lowest-numbered seed task,
// which the caller uses as the link anchor for task_event_links.
//
// The sqlc Queries handle is no longer needed here: taskcreate.New takes
// the transaction directly. The parameter stays so the seed helpers keep a
// uniform signature.
func ensureTasks(ctx context.Context, db *sql.DB, _ *generated.Queries, wsID, projID, userID uint32, l seedLocale, logger *slog.Logger) (uint32, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND project_id = ? AND enabled = TRUE",
		wsID, projID,
	).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		createdBy := taskcreate.AuthoredBy(userID)
		if err := dbretry.InTx(ctx, db, "seed.ensureTasks", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			for _, s := range l.Tasks {
				title, terr := taskrules.NewTitle(s.Title)
				if terr != nil {
					return fmt.Errorf("create seed task: %w", terr)
				}
				if _, err := taskcreate.New(ctx, tx, createdBy, taskcreate.Args{
					WorkspaceID: wsID,
					ProjectID:   projID,
					Title:       title,
					Priority:    s.Priority,
				}); err != nil {
					return fmt.Errorf("create seed task: %w", err)
				}
			}
			return nil
		}); err != nil {
			return 0, err
		}
		logger.Info("created seed tasks", "project_id", projID, "count", len(l.Tasks))
	} else {
		logger.Info("tasks exist, skipping", "project_id", projID, "count", count)
	}
	var firstID uint32
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM tasks
		 WHERE workspace_id = ? AND project_id = ? AND enabled = TRUE
		 ORDER BY task_number ASC, id ASC LIMIT 1`,
		wsID, projID,
	).Scan(&firstID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return firstID, nil
}

// ensureProjectMember upserts a project_members row for the given user
// on the given project. Idempotent on (project_id, user_id): a re-run
// updates role/enabled to match the requested values without raising a
// duplicate-key error. Uses BINARY(16) UUID v7 like other seed rows.
func ensureProjectMember(ctx context.Context, db *sql.DB, projectID, userID uint32, role generated.ProjectMembersRole, logger *slog.Logger) error {
	// Look up the workspace_id from the project so the insert keeps the
	// (workspace_id, project_id, user_id) triangle consistent without
	// asking callers to pass it again.
	var wsID uint32
	if err := db.QueryRowContext(ctx,
		"SELECT workspace_id FROM projects WHERE id = ?",
		projectID,
	).Scan(&wsID); err != nil {
		return fmt.Errorf("lookup project workspace: %w", err)
	}
	pub := types.New()
	now := time.Now()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO project_members
		   (public_id, workspace_id, project_id, user_id, role, added_at, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, TRUE)
		 ON DUPLICATE KEY UPDATE
		   role = VALUES(role),
		   enabled = TRUE,
		   added_at = COALESCE(project_members.added_at, VALUES(added_at))`,
		pub, wsID, projectID, userID, string(role), now,
	); err != nil {
		return err
	}
	logger.Info("ensured project member", "project_id", projectID, "user_id", userID, "role", string(role))
	return nil
}

func ensureTaskEventLink(ctx context.Context, q *generated.Queries, wsID, taskID, eventID uint32, logger *slog.Logger) error {
	relation := generated.TaskEventLinksRelationContributesTo
	existing, err := q.FindActiveLink(ctx, generated.FindActiveLinkParams{
		WorkspaceID: wsID,
		TaskID:      taskID,
		EventID:     eventID,
		Relation:    relation,
	})
	if err == nil {
		logger.Info("task-event link exists", "link_id", existing.ID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	id, err := q.CreateTaskEventLink(ctx, generated.CreateTaskEventLinkParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		TaskID:      taskID,
		EventID:     eventID,
		Relation:    relation,
		SortWeight:  0,
	})
	if err != nil {
		return err
	}
	logger.Info("created task-event link", "id", id, "task_id", taskID, "event_id", eventID)
	return nil
}

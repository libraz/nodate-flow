package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func ensureUser(ctx context.Context, q *generated.Queries, email, displayName, locale string) (int64, bool, error) {
	row, err := q.FindUserByEmail(ctx, email)
	if err == nil {
		return int64(row.ID), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	id, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        types.New(),
		Email:           email,
		DisplayName:     displayName,
		Locale:          locale,
		Timezone:        "UTC",
		Country:         sql.NullString{},
		ThemePreference: generated.UsersThemePreference("system"),
	})
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func createLocalIdentity(ctx context.Context, q *generated.Queries, userID uint32, email, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       userID,
		Provider:     generated.IdentitiesProvider("local"),
		Subject:      email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
	}); err != nil {
		return err
	}
	return nil
}

func ensureWorkspace(ctx context.Context, db *sql.DB, q *generated.Queries, cfg seedConfig) (int64, bool, error) {
	var id uint32
	err := db.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE slug = ?", cfg.workspaceSlug).Scan(&id)
	if err == nil {
		return int64(id), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	newID, err := q.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
		PublicID: types.New(),
		Slug:     cfg.workspaceSlug,
		Name:     cfg.workspaceName,
		Timezone: "UTC",
		Country:  sql.NullString{},
	})
	if err != nil {
		return 0, false, err
	}
	return newID, true, nil
}

func ensureMembership(ctx context.Context, db *sql.DB, q *generated.Queries, wsID, userID uint32, role generated.WorkspaceMembersRole, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM workspace_members WHERE workspace_id = ? AND user_id = ?",
		wsID, userID,
	).Scan(&existing)
	if err == nil {
		logger.Info("workspace membership exists", "workspace_id", wsID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := sql.NullTime{Time: time.Now(), Valid: true}
	if _, err := q.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		UserID:      userID,
		Role:        role,
		JoinedAt:    now,
	}); err != nil {
		return err
	}
	logger.Info("created workspace membership", "workspace_id", wsID, "user_id", userID, "role", string(role))
	return nil
}

func ensureInstanceAdmin(ctx context.Context, db *sql.DB, userID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM instance_admins WHERE user_id = ?", userID,
	).Scan(&existing)
	if err == nil {
		logger.Info("instance admin exists", "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	pub := types.New()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO instance_admins (public_id, user_id) VALUES (?, ?)",
		pub, userID,
	); err != nil {
		return err
	}
	logger.Info("created instance admin grant", "user_id", userID)
	return nil
}

// Command seed-dev inserts a minimum set of rows to make a fresh
// nodate-flow database usable for local development: one user (with a
// local password identity), one workspace, one workspace_members owner
// grant, and one instance_admins grant so the user can reach admin UIs.
//
// Usage:
//
//	ND_DB_DSN=... \
//	ND_SEED_LOCALE=en \
//	ND_SEED_EMAIL=admin@example.com \
//	ND_SEED_PASSWORD=password123 \
//	ND_SEED_DISPLAY_NAME=Admin \
//	ND_SEED_WORKSPACE_SLUG=demo \
//	ND_SEED_WORKSPACE_NAME="Demo Workspace" \
//	  go run ./cmd/seed-dev
//
// ND_SEED_LOCALE selects the language for display names, project names,
// and task titles. Supported values: "en" (default), "ja".
//
// Re-running is safe: existing rows (matched by email / slug) are
// detected and the command becomes a no-op with an informational log.
// This is a development helper ONLY - never run against production.
package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

//go:embed locales/*.json
var localesFS embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(context.Background(), logger); err != nil {
		logger.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

type seedConfig struct {
	dsn           string
	email         string
	password      string
	displayName   string
	workspaceSlug string
	workspaceName string
	locale        string
}

// seedLocale holds locale-specific default values for seed data.
// Loaded from locales/*.json via embed.FS.
type seedLocale struct {
	DisplayName   string     `json:"displayName"`
	WorkspaceName string     `json:"workspaceName"`
	ProjectName   string     `json:"projectName"`
	ProjectDesc   string     `json:"projectDesc"`
	Tasks         []seedTask `json:"tasks"`
}

type seedTask struct {
	Title    string `json:"title"`
	Priority int32  `json:"priority"`
}

func loadLocale(name string) (seedLocale, error) {
	data, err := localesFS.ReadFile("locales/" + name + ".json")
	if err != nil {
		return seedLocale{}, fmt.Errorf("unsupported ND_SEED_LOCALE %q (add locales/%s.json to support it)", name, name)
	}
	var l seedLocale
	if err := json.Unmarshal(data, &l); err != nil {
		return seedLocale{}, fmt.Errorf("parse locales/%s.json: %w", name, err)
	}
	return l, nil
}

func loadConfig() (seedConfig, seedLocale, error) {
	locale := envOr("ND_SEED_LOCALE", "en")
	l, err := loadLocale(locale)
	if err != nil {
		return seedConfig{}, seedLocale{}, err
	}
	c := seedConfig{
		dsn:           os.Getenv("ND_DB_DSN"),
		email:         envOr("ND_SEED_EMAIL", "admin@example.com"),
		password:      envOr("ND_SEED_PASSWORD", "password123"),
		displayName:   envOr("ND_SEED_DISPLAY_NAME", l.DisplayName),
		workspaceSlug: envOr("ND_SEED_WORKSPACE_SLUG", "demo"),
		workspaceName: envOr("ND_SEED_WORKSPACE_NAME", l.WorkspaceName),
		locale:        locale,
	}
	if c.dsn == "" {
		return c, seedLocale{}, errors.New("ND_DB_DSN is required")
	}
	if len(c.password) < 8 {
		return c, seedLocale{}, errors.New("ND_SEED_PASSWORD must be >= 8 chars")
	}
	return c, l, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, l, err := loadConfig()
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", cfg.dsn)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}

	q := generated.New(db)

	// 1. User (idempotent on email).
	userID, created, err := ensureUser(ctx, q, cfg)
	if err != nil {
		return fmt.Errorf("ensure user: %w", err)
	}
	if created {
		logger.Info("created user", "email", cfg.email, "id", userID)
	} else {
		logger.Info("user exists", "email", cfg.email, "id", userID)
	}

	// 2. Local identity (skip if user already existed - we don't want
	// to overwrite a password that might be newer than the default).
	if created {
		hash, err := auth.HashPassword(cfg.password)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		if _, err := q.CreateIdentity(ctx, generated.CreateIdentityParams{
			PublicID:     types.New(),
			UserID:       uint32(userID),
			Provider:     generated.IdentitiesProvider("local"),
			Subject:      cfg.email,
			PasswordHash: sql.NullString{String: hash, Valid: true},
		}); err != nil {
			return fmt.Errorf("create identity: %w", err)
		}
		logger.Info("created local identity", "email", cfg.email)
	}

	// 3. Workspace (idempotent on slug).
	wsID, wsCreated, err := ensureWorkspace(ctx, db, q, cfg)
	if err != nil {
		return fmt.Errorf("ensure workspace: %w", err)
	}
	if wsCreated {
		logger.Info("created workspace", "slug", cfg.workspaceSlug, "id", wsID)
	} else {
		logger.Info("workspace exists", "slug", cfg.workspaceSlug, "id", wsID)
	}

	// 4. workspace_members (owner) - idempotent on (workspace_id, user_id).
	if err := ensureMembership(ctx, db, q, uint32(wsID), uint32(userID), logger); err != nil {
		return fmt.Errorf("ensure membership: %w", err)
	}

	// 5. instance_admins - idempotent on user_id.
	if err := ensureInstanceAdmin(ctx, db, uint32(userID), logger); err != nil {
		return fmt.Errorf("ensure instance admin: %w", err)
	}

	// 6. Demo project + tasks (idempotent on project slug).
	projID, projCreated, err := ensureProject(ctx, db, q, uint32(wsID), l)
	if err != nil {
		return fmt.Errorf("ensure project: %w", err)
	}
	if projCreated {
		logger.Info("created project", "id", projID)
	} else {
		logger.Info("project exists", "id", projID)
	}
	if err := ensureTasks(ctx, db, q, uint32(wsID), uint32(projID), uint32(userID), l, logger); err != nil {
		return fmt.Errorf("ensure tasks: %w", err)
	}

	logger.Info("seed complete",
		"email", cfg.email,
		"password", cfg.password,
		"workspace", cfg.workspaceSlug,
	)
	return nil
}

func ensureUser(ctx context.Context, q *generated.Queries, cfg seedConfig) (int64, bool, error) {
	row, err := q.FindUserByEmail(ctx, cfg.email)
	if err == nil {
		return int64(row.ID), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	id, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        types.New(),
		Email:           cfg.email,
		DisplayName:     cfg.displayName,
		Locale:          cfg.locale,
		ThemePreference: generated.UsersThemePreference("system"),
	})
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
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
	})
	if err != nil {
		return 0, false, err
	}
	return newID, true, nil
}

func ensureMembership(ctx context.Context, db *sql.DB, q *generated.Queries, wsID, userID uint32, logger *slog.Logger) error {
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
		Role:        generated.WorkspaceMembersRole("owner"),
		JoinedAt:    now,
	}); err != nil {
		return err
	}
	logger.Info("created workspace membership", "workspace_id", wsID, "user_id", userID, "role", "owner")
	return nil
}

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

func ensureTasks(ctx context.Context, db *sql.DB, q *generated.Queries, wsID, projID, userID uint32, l seedLocale, logger *slog.Logger) error {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND project_id = ? AND enabled = TRUE",
		wsID, projID,
	).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		logger.Info("tasks exist, skipping", "project_id", projID, "count", count)
		return nil
	}
	createdBy := sql.NullInt32{Int32: int32(userID), Valid: true}
	for _, s := range l.Tasks {
		if _, err := q.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        types.New(),
			WorkspaceID:     wsID,
			ProjectID:       projID,
			CreatedByUserID: createdBy,
			Title:           s.Title,
			Priority:        s.Priority,
			Visibility:      generated.TasksVisibilityPublic,
		}); err != nil {
			return err
		}
	}
	logger.Info("created seed tasks", "project_id", projID, "count", len(l.Tasks))
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

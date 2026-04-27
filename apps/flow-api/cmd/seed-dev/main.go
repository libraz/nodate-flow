// Command seed-dev inserts a minimum set of rows to make a fresh
// nodate-flow database usable for local development:
//
//   - two users (one owner "admin" with instance-admin + one member
//     "alice"), each with a local password identity;
//   - one workspace, with both users as workspace_members;
//   - a personal calendar + subscription for each user;
//   - a JP-holidays system calendar with a sample holiday and a
//     subscription for the owner;
//   - one dated event on the owner's calendar with the second user as
//     an attendee (RSVP pending);
//   - one undated event (start_at NULL) demonstrating date-free items;
//   - a demo project + five tasks, the first of which is linked to the
//     dated event via task_event_links (relation 'contributes_to');
//   - a workspace-owned public share page with the dated event
//     attached so the /share/cal/{token} render path has something to
//     display;
//   - a placeholder Anthropic ai_providers row + matching ai_models row
//     so the /settings/ai-agents create flow has something to bind to
//     on a fresh DB. The api_key_ciphertext is a seed placeholder;
//     rotate via PATCH /ai/providers before any real LLM dispatch.
//
// Usage:
//
//	NF_DB_DSN=... \
//	NF_SEED_LOCALE=en \
//	NF_SEED_EMAIL=admin@example.com \
//	NF_SEED_PASSWORD=password123 \
//	NF_SEED_DISPLAY_NAME=Admin \
//	NF_SEED_USER2_EMAIL=alice@example.com \
//	NF_SEED_USER2_PASSWORD=password123 \
//	NF_SEED_USER2_DISPLAY_NAME=Alice \
//	NF_SEED_WORKSPACE_SLUG=demo \
//	NF_SEED_WORKSPACE_NAME="Demo Workspace" \
//	  go run ./cmd/seed-dev
//
// NF_SEED_LOCALE selects the language for display names, project names,
// and task titles. Supported values: "en" (default), "ja".
//
// Re-running is safe: existing rows (matched by email / slug / title)
// are detected and the command becomes a no-op with an informational
// log. This is a development helper ONLY - never run against production.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
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
	dsn              string
	email            string
	password         string
	displayName      string
	user2Email       string
	user2Password    string
	user2DisplayName string
	workspaceSlug    string
	workspaceName    string
	locale           string
}

// seedLocale holds locale-specific default values for seed data.
// Loaded from locales/*.json via embed.FS.
type seedLocale struct {
	DisplayName            string     `json:"displayName"`
	WorkspaceName          string     `json:"workspaceName"`
	ProjectName            string     `json:"projectName"`
	ProjectDesc            string     `json:"projectDesc"`
	SecondUserDisplayName  string     `json:"secondUserDisplayName"`
	HolidaysCalendarName   string     `json:"holidaysCalendarName"`
	HolidayEventTitle      string     `json:"holidayEventTitle"`
	SampleEventTitle       string     `json:"sampleEventTitle"`
	SampleEventLocation    string     `json:"sampleEventLocation"`
	UndatedEventTitle      string     `json:"undatedEventTitle"`
	UndatedEventMemo       string     `json:"undatedEventMemo"`
	PublicShareTitle       string     `json:"publicShareTitle"`
	PublicShareDescription string     `json:"publicShareDescription"`
	Tasks                  []seedTask `json:"tasks"`
}

type seedTask struct {
	Title    string `json:"title"`
	Priority int32  `json:"priority"`
}

func loadLocale(name string) (seedLocale, error) {
	data, err := localesFS.ReadFile("locales/" + name + ".json")
	if err != nil {
		return seedLocale{}, fmt.Errorf("unsupported NF_SEED_LOCALE %q (add locales/%s.json to support it)", name, name)
	}
	var l seedLocale
	if err := json.Unmarshal(data, &l); err != nil {
		return seedLocale{}, fmt.Errorf("parse locales/%s.json: %w", name, err)
	}
	return l, nil
}

func loadConfig() (seedConfig, seedLocale, error) {
	locale := envOr("NF_SEED_LOCALE", "en")
	l, err := loadLocale(locale)
	if err != nil {
		return seedConfig{}, seedLocale{}, err
	}
	c := seedConfig{
		dsn:              os.Getenv("NF_DB_DSN"),
		email:            envOr("NF_SEED_EMAIL", "admin@example.com"),
		password:         envOr("NF_SEED_PASSWORD", "password123"),
		displayName:      envOr("NF_SEED_DISPLAY_NAME", l.DisplayName),
		user2Email:       envOr("NF_SEED_USER2_EMAIL", "alice@example.com"),
		user2Password:    envOr("NF_SEED_USER2_PASSWORD", "password123"),
		user2DisplayName: envOr("NF_SEED_USER2_DISPLAY_NAME", l.SecondUserDisplayName),
		workspaceSlug:    envOr("NF_SEED_WORKSPACE_SLUG", "demo"),
		workspaceName:    envOr("NF_SEED_WORKSPACE_NAME", l.WorkspaceName),
		locale:           locale,
	}
	if c.dsn == "" {
		return c, seedLocale{}, errors.New("NF_DB_DSN is required")
	}
	if len(c.password) < 8 {
		return c, seedLocale{}, errors.New("NF_SEED_PASSWORD must be >= 8 chars")
	}
	if len(c.user2Password) < 8 {
		return c, seedLocale{}, errors.New("NF_SEED_USER2_PASSWORD must be >= 8 chars")
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
	// cq is the dedicated calendar sqlc subpackage handle, used by the
	// calendar-event seed steps below. Shares the same *sql.DB pool as q.
	cq := calendar.New(db)

	// 1. Owner user (idempotent on email).
	ownerID, created, err := ensureUser(ctx, q, cfg.email, cfg.displayName, cfg.locale)
	if err != nil {
		return fmt.Errorf("ensure owner: %w", err)
	}
	if created {
		logger.Info("created user", "email", cfg.email, "id", ownerID)
		if err := createLocalIdentity(ctx, q, uint32(ownerID), cfg.email, cfg.password); err != nil { //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in dev seed
			return fmt.Errorf("owner identity: %w", err)
		}
		logger.Info("created local identity", "email", cfg.email)
	} else {
		logger.Info("user exists", "email", cfg.email, "id", ownerID)
	}

	// 2. Workspace (idempotent on slug).
	wsID, wsCreated, err := ensureWorkspace(ctx, db, q, cfg)
	if err != nil {
		return fmt.Errorf("ensure workspace: %w", err)
	}
	if wsCreated {
		logger.Info("created workspace", "slug", cfg.workspaceSlug, "id", wsID)
	} else {
		logger.Info("workspace exists", "slug", cfg.workspaceSlug, "id", wsID)
	}

	// 3. Owner membership + instance admin grant.
	if err := ensureMembership(ctx, db, q, uint32(wsID), uint32(ownerID), generated.WorkspaceMembersRoleOwner, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure owner membership: %w", err)
	}
	if err := ensureInstanceAdmin(ctx, db, uint32(ownerID), logger); err != nil { //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in dev seed
		return fmt.Errorf("ensure instance admin: %w", err)
	}

	// 4. Owner personal calendar + subscription.
	ownerCalID, err := ensurePersonalCalendar(ctx, db, uint32(wsID), uint32(ownerID), cfg.workspaceName, logger) //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure owner calendar: %w", err)
	}
	if err := ensureSubscription(ctx, db, uint32(wsID), ownerCalID, uint32(ownerID), logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure owner subscription: %w", err)
	}

	// 5. Second user (idempotent on email).
	secondID, secondCreated, err := ensureUser(ctx, q, cfg.user2Email, cfg.user2DisplayName, cfg.locale)
	if err != nil {
		return fmt.Errorf("ensure second user: %w", err)
	}
	if secondCreated {
		logger.Info("created user", "email", cfg.user2Email, "id", secondID)
		if err := createLocalIdentity(ctx, q, uint32(secondID), cfg.user2Email, cfg.user2Password); err != nil { //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in dev seed
			return fmt.Errorf("second identity: %w", err)
		}
		logger.Info("created local identity", "email", cfg.user2Email)
	} else {
		logger.Info("user exists", "email", cfg.user2Email, "id", secondID)
	}
	if err := ensureMembership(ctx, db, q, uint32(wsID), uint32(secondID), generated.WorkspaceMembersRoleMember, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure second membership: %w", err)
	}
	secondCalID, err := ensurePersonalCalendar(ctx, db, uint32(wsID), uint32(secondID), cfg.user2DisplayName, logger) //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure second calendar: %w", err)
	}
	if err := ensureSubscription(ctx, db, uint32(wsID), secondCalID, uint32(secondID), logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure second subscription: %w", err)
	}

	// 6. JP holidays system calendar (subscribed by owner).
	holidayCalID, err := ensureHolidayCalendar(ctx, db, uint32(wsID), l.HolidaysCalendarName, logger) //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure holiday calendar: %w", err)
	}
	if err := ensureSubscription(ctx, db, uint32(wsID), holidayCalID, uint32(ownerID), logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure holiday subscription: %w", err)
	}
	if err := ensureHolidayEvent(ctx, db, cq, uint32(wsID), holidayCalID, uint32(ownerID), l.HolidayEventTitle, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure holiday event: %w", err)
	}

	// 7. Demo project + tasks.
	projID, projCreated, err := ensureProject(ctx, db, q, uint32(wsID), l) //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure project: %w", err)
	}
	if projCreated {
		logger.Info("created project", "id", projID)
	} else {
		logger.Info("project exists", "id", projID)
	}
	firstTaskID, err := ensureTasks(ctx, db, q, uint32(wsID), uint32(projID), uint32(ownerID), l, logger) //#nosec G115 -- LastInsertIds for workspaces.id, projects.id, users.id fit uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure tasks: %w", err)
	}

	// 8. Owner's sample event with second-user attendee.
	sampleEventID, err := ensureSampleEvent(ctx, db, cq, uint32(wsID), ownerCalID, uint32(ownerID), l.SampleEventTitle, l.SampleEventLocation, logger) //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure sample event: %w", err)
	}
	if err := ensureAttendee(ctx, db, cq, uint32(wsID), sampleEventID, uint32(secondID), logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
		return fmt.Errorf("ensure attendee: %w", err)
	}

	// 9. Undated event on owner's calendar.
	if _, err := ensureUndatedEvent(ctx, db, cq, uint32(wsID), ownerCalID, uint32(ownerID), l.UndatedEventTitle, l.UndatedEventMemo, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
		return fmt.Errorf("ensure undated event: %w", err)
	}

	// 10. Task-event link: first task contributes_to the sample event.
	if firstTaskID > 0 {
		if err := ensureTaskEventLink(ctx, q, uint32(wsID), uint32(firstTaskID), sampleEventID, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and tasks.id fit uint32 in dev seed
			return fmt.Errorf("ensure task-event link: %w", err)
		}
	}

	// 11. Workspace public share + attach the sample event.
	shareID, err := ensurePublicShare(ctx, db, cq, uint32(wsID), uint32(ownerID), l.PublicShareTitle, l.PublicShareDescription, logger) //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure public share: %w", err)
	}
	if err := ensureShareEvent(ctx, db, cq, uint32(wsID), shareID, sampleEventID, logger); err != nil { //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in dev seed
		return fmt.Errorf("ensure share event: %w", err)
	}

	// 12. AI provider + model (placeholder key). Gives the AI-agents
	// settings page at least one row so operators can exercise the
	// create-agent flow on a fresh dev DB. The api_key_ciphertext is a
	// seed placeholder; rotate it via PATCH /ai/providers before the
	// provider actually dispatches to an upstream LLM.
	if err := ensureAIProviderAndModel(ctx, db, uint32(wsID), logger); err != nil { //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in dev seed
		return fmt.Errorf("ensure ai provider + model: %w", err)
	}

	logger.Info("seed complete",
		"email", cfg.email,
		"password", cfg.password,
		"user2_email", cfg.user2Email,
		"user2_password", cfg.user2Password,
		"workspace", cfg.workspaceSlug,
	)
	return nil
}

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
func ensureTasks(ctx context.Context, db *sql.DB, q *generated.Queries, wsID, projID, userID uint32, l seedLocale, logger *slog.Logger) (uint32, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND project_id = ? AND enabled = TRUE",
		wsID, projID,
	).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		createdBy := sql.NullInt32{Int32: int32(userID), Valid: true} //#nosec G115 -- user id sourced from seed flow, fits int32
		for _, s := range l.Tasks {
			nextNum, err := q.AssignTaskNumber(ctx, generated.AssignTaskNumberParams{
				WorkspaceID: wsID,
				ProjectID:   projID,
			})
			if err != nil {
				return 0, fmt.Errorf("assign task number: %w", err)
			}
			if _, err := q.CreateTask(ctx, generated.CreateTaskParams{
				PublicID:        types.New(),
				WorkspaceID:     wsID,
				ProjectID:       projID,
				CreatedByUserID: createdBy,
				UpdatedByUserID: createdBy,
				TaskNumber:      uint32(nextNum), //#nosec G115 -- task_number is per-project sequence, fits uint32
				Title:           s.Title,
				Priority:        s.Priority,
				Visibility:      generated.TasksVisibilityPublic,
			}); err != nil {
				return 0, err
			}
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

func ensurePersonalCalendar(ctx context.Context, db *sql.DB, wsID, userID uint32, name string, logger *slog.Logger) (uint32, error) {
	var calID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendars
		 WHERE workspace_id = ? AND kind = 'personal' AND owner_user_id = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, userID,
	).Scan(&calID)
	if err == nil {
		logger.Info("calendar exists", "calendar_id", calID, "user_id", userID)
		return calID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	pub := types.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, owner_user_id)
		 VALUES (?, ?, 'personal', ?, '#4285F4', ?)`,
		pub, wsID, name, userID,
	)
	if err != nil {
		return 0, err
	}
	newID64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	logger.Info("created personal calendar", "id", newID64, "user_id", userID)
	return uint32(newID64), nil //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureHolidayCalendar(ctx context.Context, db *sql.DB, wsID uint32, name string, logger *slog.Logger) (uint32, error) {
	const slug = "holidays.jp"
	var calID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendars
		 WHERE workspace_id = ? AND kind = 'system' AND system_slug = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, slug,
	).Scan(&calID)
	if err == nil {
		logger.Info("holiday calendar exists", "calendar_id", calID)
		return calID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	pub := types.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, system_slug)
		 VALUES (?, ?, 'system', ?, '#DC2626', ?)`,
		pub, wsID, name, slug,
	)
	if err != nil {
		return 0, err
	}
	newID64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	logger.Info("created holiday calendar", "id", newID64, "slug", slug)
	return uint32(newID64), nil //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureSubscription(ctx context.Context, db *sql.DB, wsID, calID, userID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM calendar_subscriptions WHERE calendar_id = ? AND user_id = ? AND workspace_id = ?",
		calID, userID, wsID,
	).Scan(&existing)
	if err == nil {
		logger.Info("calendar subscription exists", "calendar_id", calID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	pub := types.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO calendar_subscriptions (public_id, calendar_id, user_id, workspace_id, display_color, visible, sort_weight)
		 VALUES (?, ?, ?, ?, '#4285F4', TRUE, 0)`,
		pub, calID, userID, wsID,
	); err != nil {
		return err
	}
	logger.Info("created calendar subscription", "calendar_id", calID, "user_id", userID)
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

// findEventIDByTitle matches seed events by (calendar_id, title) since
// calendar_events has no slug. Seed titles are unique per calendar.
func findEventIDByTitle(ctx context.Context, db *sql.DB, calID uint32, title string) (uint32, error) {
	var id uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_events
		 WHERE calendar_id = ? AND title = ? AND enabled = TRUE
		 ORDER BY id ASC LIMIT 1`,
		calID, title,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// nextNoonUTC returns today at 12:00 UTC if the current time is before
// that, otherwise tomorrow at 12:00 UTC. Keeps the sample event in the
// future for a consistent demo.
func nextNoonUTC() time.Time {
	now := time.Now().UTC()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if !noon.After(now) {
		noon = noon.Add(24 * time.Hour)
	}
	return noon
}

func ensureSampleEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title, location string, logger *slog.Logger) (uint32, error) {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("sample event exists", "id", existing, "title", title)
		return existing, nil
	}
	start := nextNoonUTC()
	end := start.Add(time.Hour)
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindEvent,
		Visibility:      calendar.CalendarEventsVisibilityPublic,
		ShowAs:          calendar.CalendarEventsShowAsBusy,
		Title:           title,
		AllDay:          false,
		StartAt:         sql.NullTime{Time: start, Valid: true},
		EndAt:           sql.NullTime{Time: end, Valid: true},
		Timezone:        "UTC",
		Location:        sql.NullString{String: location, Valid: location != ""},
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created sample event", "id", id, "title", title, "start_at", start.Format(time.RFC3339))
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_events.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureUndatedEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title, memo string, logger *slog.Logger) (uint32, error) {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("undated event exists", "id", existing, "title", title)
		return existing, nil
	}
	// Probe whether calendar_events.start_at is nullable in the currently
	// connected MySQL instance. Older local containers were booted against
	// an earlier schema that declared start_at as NOT NULL, and compose
	// only seeds schema.sql on an empty data volume — so stale DBs survive
	// schema edits silently. Rather than hard-failing the entire seed run,
	// warn the operator and skip this single fixture; a fresh
	// `make db-reset` (or `make db-apply` for self-hosted MySQL) picks up
	// the nullable column and restores the undated-event fixture on the
	// next run.
	var isNullable string
	probeErr := db.QueryRowContext(ctx, `
		SELECT IS_NULLABLE
		FROM information_schema.columns
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = 'calendar_events'
		  AND COLUMN_NAME = 'start_at'
	`).Scan(&isNullable)
	if probeErr != nil {
		return 0, fmt.Errorf("probe calendar_events.start_at nullability: %w", probeErr)
	}
	if isNullable != "YES" {
		logger.Warn(
			"skipping undated event: calendar_events.start_at is NOT NULL in this DB; run `make db-reset` (or `make db-apply` for self-hosted MySQL) to pick up the nullable schema",
			"title", title,
		)
		return 0, nil
	}
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindEvent,
		Visibility:      calendar.CalendarEventsVisibilityPrivate,
		ShowAs:          calendar.CalendarEventsShowAsFree,
		Title:           title,
		AllDay:          false,
		StartAt:         sql.NullTime{},
		EndAt:           sql.NullTime{},
		Timezone:        "UTC",
		Memo:            sql.NullString{String: memo, Valid: memo != ""},
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created undated event", "id", id, "title", title)
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_events.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureHolidayEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title string, logger *slog.Logger) error {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return err
	} else if existing > 0 {
		logger.Info("holiday event exists", "id", existing, "title", title)
		return nil
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindBlock,
		Visibility:      calendar.CalendarEventsVisibilityPublic,
		ShowAs:          calendar.CalendarEventsShowAsFree,
		Title:           title,
		AllDay:          true,
		StartAt:         sql.NullTime{Time: start, Valid: true},
		EndAt:           sql.NullTime{Time: end, Valid: true},
		Timezone:        "UTC",
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return err
	}
	logger.Info("created holiday event", "id", id, "title", title)
	return nil
}

func ensureAttendee(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, eventID, userID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_event_attendees
		 WHERE event_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`,
		eventID, userID,
	).Scan(&existing)
	if err == nil {
		logger.Info("attendee exists", "event_id", eventID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := cq.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		EventID:     eventID,
		UserID:      userID,
		Rsvp:        calendar.CalendarEventAttendeesRsvpPending,
		CanEdit:     false,
	}); err != nil {
		return err
	}
	logger.Info("created attendee", "event_id", eventID, "user_id", userID)
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

// findShareIDByTitle matches seed shares by (workspace_id, title) so
// re-runs find the prior row without exposing the plaintext token.
func findShareIDByTitle(ctx context.Context, db *sql.DB, wsID uint32, title string) (uint32, error) {
	var id uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_public_shares
		 WHERE workspace_id = ? AND title = ? AND enabled = TRUE
		 ORDER BY id ASC LIMIT 1`,
		wsID, title,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

func ensurePublicShare(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, ownerID uint32, title, description string, logger *slog.Logger) (uint32, error) {
	if existing, err := findShareIDByTitle(ctx, db, wsID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("public share exists", "id", existing, "title", title)
		return existing, nil
	}
	token, err := mintShareToken()
	if err != nil {
		return 0, fmt.Errorf("mint token: %w", err)
	}
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	id, err := cq.CreatePublicShare(ctx, calendar.CreatePublicShareParams{
		PublicID:            types.New(),
		WorkspaceID:         wsID,
		CreatedByUserID:     sql.NullInt32{Int32: int32(ownerID), Valid: true}, //#nosec G115 -- owner id sourced from seed flow, fits int32
		TokenHash:           tokenHash,
		Title:               title,
		Description:         sql.NullString{String: description, Valid: description != ""},
		Timezone:            "UTC",
		ShowHolidaysCountry: sql.NullString{String: "JP", Valid: true},
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created public share", "id", id, "title", title, "token", token)
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_public_shares.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureShareEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, shareID, eventID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_public_share_events
		 WHERE share_id = ? AND event_id = ? AND enabled = TRUE LIMIT 1`,
		shareID, eventID,
	).Scan(&existing)
	if err == nil {
		logger.Info("share event exists", "share_id", shareID, "event_id", eventID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := cq.AttachEventToShare(ctx, calendar.AttachEventToShareParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		ShareID:     shareID,
		EventID:     eventID,
		SortWeight:  0,
	}); err != nil {
		return err
	}
	logger.Info("attached event to share", "share_id", shareID, "event_id", eventID)
	return nil
}

// mintShareToken mirrors the handler's token minting (24 random bytes
// -> RawURLEncoding -> SHA-256) so seeded shares look like real ones.
func mintShareToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// seedAIProviderKind / seedAIProviderName / seedAIModelName are the
// defaults used by ensureAIProviderAndModel. They are intentionally
// scoped to this file so ops tooling changes do not ripple into the
// real create-provider handler.
const (
	seedAIProviderKind = "anthropic"
	seedAIProviderName = "Anthropic (seed placeholder)"
	seedAIModelName    = "claude-sonnet-4-6"
)

// ensureAIProviderAndModel inserts an ai_providers row and a matching
// ai_models row so the AI-agents settings page has something to bind
// against on a freshly seeded dev DB. The api_key_ciphertext is a
// deterministic-looking random blob and the prefix/suffix flag the row
// as a seed placeholder. Rotating the key via PATCH /ai/providers turns
// the provider into a real one. Idempotent on (workspace_id, kind, name).
func ensureAIProviderAndModel(ctx context.Context, db *sql.DB, wsID uint32, logger *slog.Logger) error {
	var providerID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM ai_providers
		 WHERE workspace_id = ? AND kind = ? AND name = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, seedAIProviderKind, seedAIProviderName,
	).Scan(&providerID)
	switch {
	case err == nil:
		logger.Info("ai provider exists", "provider_id", providerID)
	case errors.Is(err, sql.ErrNoRows):
		pub := types.New()
		// 32 random bytes stand in for AES-GCM output. The provider row
		// will not be usable for real LLM dispatch until an operator
		// rotates the key via the PATCH endpoint.
		ct := make([]byte, 32)
		if _, err := rand.Read(ct); err != nil {
			return fmt.Errorf("rand ct: %w", err)
		}
		res, err := db.ExecContext(ctx,
			`INSERT INTO ai_providers (
				public_id, workspace_id, kind, name,
				api_key_ciphertext, api_key_prefix, api_key_suffix,
				default_model
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			pub, wsID, seedAIProviderKind, seedAIProviderName,
			ct, "sk-SEED-", "SEED", seedAIModelName,
		)
		if err != nil {
			return fmt.Errorf("insert ai provider: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("ai provider last id: %w", err)
		}
		providerID = uint32(newID) //#nosec G115 -- LastInsertId for ai_providers.id (BIGINT UNSIGNED), fits uint32 in dev seed
		logger.Info("created ai provider", "id", providerID, "kind", seedAIProviderKind,
			"note", "api_key_ciphertext is a seed placeholder; rotate via PATCH before real use")
	default:
		return fmt.Errorf("lookup ai provider: %w", err)
	}

	// ai_models row matching the provider's default_model. Unique key is
	// (provider_id, name) so re-runs become no-ops.
	var modelID uint32
	err = db.QueryRowContext(ctx,
		`SELECT id FROM ai_models
		 WHERE provider_id = ? AND name = ? AND enabled = TRUE
		 LIMIT 1`,
		providerID, seedAIModelName,
	).Scan(&modelID)
	switch {
	case err == nil:
		logger.Info("ai model exists", "model_id", modelID)
		return nil
	case errors.Is(err, sql.ErrNoRows):
		pub := types.New()
		res, err := db.ExecContext(ctx,
			`INSERT INTO ai_models (
				public_id, workspace_id, provider_id, name, display_name,
				context_window, max_output_tokens,
				input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
				supports_tools, supports_vision, enabled
			) VALUES (?, ?, ?, ?, ?, 200000, 8192, 0, 0, TRUE, TRUE, TRUE)`,
			pub, wsID, providerID, seedAIModelName, seedAIModelName,
		)
		if err != nil {
			return fmt.Errorf("insert ai model: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("ai model last id: %w", err)
		}
		logger.Info("created ai model", "id", newID, "name", seedAIModelName)
		return nil
	default:
		return fmt.Errorf("lookup ai model: %w", err)
	}
}

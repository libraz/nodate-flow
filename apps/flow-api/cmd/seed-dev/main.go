// Command seed-dev inserts a minimum set of rows to make a fresh
// nodate-flow database usable for local development:
//
//   - two users (one owner "admin" with instance-admin + one member
//     "alice"), each with a local password identity;
//   - one workspace, with both users as workspace_members;
//   - a personal calendar for each user, with the owner membership that
//     grants access to it and a subscription holding its display
//     preferences;
//   - a JP-holidays system calendar with a sample holiday, and a viewer
//     membership + subscription for the owner;
//   - one dated event on the owner's calendar with the second user as
//     an attendee (RSVP pending);
//   - one undated event (start_at NULL) demonstrating date-free items;
//   - a demo project + five tasks, the first of which is linked to the
//     dated event via task_event_links (relation 'contributes_to');
//   - a workspace-owned public share page with the dated event
//     attached so the /share/cal/{token} render path has something to
//     display;
//   - a placeholder Anthropic ai_providers row + matching ai_models row
//     so the /settings/ai/agents create flow has something to bind to
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
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(context.Background(), logger); err != nil {
		logger.Error("seed failed", "err", err)
		os.Exit(1)
	}
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
	if err := ensureCalendarMember(ctx, db, uint32(wsID), ownerCalID, uint32(ownerID), "owner", logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure owner calendar membership: %w", err)
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
	if err := ensureCalendarMember(ctx, db, uint32(wsID), secondCalID, uint32(secondID), "owner", logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure second calendar membership: %w", err)
	}
	if err := ensureSubscription(ctx, db, uint32(wsID), secondCalID, uint32(secondID), logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure second subscription: %w", err)
	}

	// 6. JP holidays system calendar (subscribed by owner).
	holidayCalID, err := ensureHolidayCalendar(ctx, db, uint32(wsID), l.HolidaysCalendarName, logger) //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 in dev seed
	if err != nil {
		return fmt.Errorf("ensure holiday calendar: %w", err)
	}
	// The holiday feed is read-only content nobody edits, so the owner
	// joins it as a viewer: enough to reach it, no grant to write.
	if err := ensureCalendarMember(ctx, db, uint32(wsID), holidayCalID, uint32(ownerID), "viewer", logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id (BIGINT UNSIGNED), fit uint32 in dev seed
		return fmt.Errorf("ensure holiday calendar membership: %w", err)
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
	if err := ensureCalendarMember(ctx, db, uint32(wsID), ownerCalID, uint32(secondID), "editor", logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
		return fmt.Errorf("ensure second user on owner calendar: %w", err)
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

	// 12. Quick-login fixtures: one user per meaningful ACL role for the
	// dev-only quick-login UI on the login page. Each user joins the same
	// default workspace; the project-scoped two also get a row in
	// project_members on the default seeded project. All credentials are
	// fixed (no env override) — this set exists only to make role coverage
	// trivial in local dev.
	type quickLoginFixture struct {
		email       string
		displayName string
		wsRole      generated.WorkspaceMembersRole
		projectRole generated.ProjectMembersRole // empty string = no project_members row
	}
	const quickLoginPassword = "password123"
	fixtures := []quickLoginFixture{
		{email: "owner@example.com", displayName: "Owner", wsRole: generated.WorkspaceMembersRoleOwner},
		{email: "wsadmin@example.com", displayName: "WsAdmin", wsRole: generated.WorkspaceMembersRoleAdmin},
		{email: "guest@example.com", displayName: "Guest", wsRole: generated.WorkspaceMembersRoleGuest},
		{email: "projectlead@example.com", displayName: "ProjectLead", wsRole: generated.WorkspaceMembersRoleMember, projectRole: generated.ProjectMembersRoleLead},
		{email: "viewer@example.com", displayName: "ProjectViewer", wsRole: generated.WorkspaceMembersRoleMember, projectRole: generated.ProjectMembersRoleViewer},
	}
	for _, f := range fixtures {
		uid, created, err := ensureUser(ctx, q, f.email, f.displayName, cfg.locale)
		if err != nil {
			return fmt.Errorf("ensure quick-login user %s: %w", f.email, err)
		}
		if created {
			logger.Info("created user", "email", f.email, "id", uid)
			if err := createLocalIdentity(ctx, q, uint32(uid), f.email, quickLoginPassword); err != nil { //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 in dev seed
				return fmt.Errorf("quick-login identity %s: %w", f.email, err)
			}
			logger.Info("created local identity", "email", f.email)
		} else {
			logger.Info("user exists", "email", f.email, "id", uid)
		}
		if err := ensureMembership(ctx, db, q, uint32(wsID), uint32(uid), f.wsRole, logger); err != nil { //#nosec G115 -- LastInsertIds for workspaces.id and users.id fit uint32 in dev seed
			return fmt.Errorf("ensure quick-login membership %s: %w", f.email, err)
		}
		if f.projectRole != "" {
			if err := ensureProjectMember(ctx, db, uint32(projID), uint32(uid), f.projectRole, logger); err != nil { //#nosec G115 -- LastInsertIds for projects.id and users.id fit uint32 in dev seed
				return fmt.Errorf("ensure project member %s: %w", f.email, err)
			}
		}
	}

	// 13. AI provider + model (placeholder key). Gives the AI-agents
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

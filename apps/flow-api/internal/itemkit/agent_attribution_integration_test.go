package itemkit

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

func TestAgentRenameAndRescheduleKeepAttributionAfterHumanScheduling(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)
	agentID := seedItemAgent(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	var eventID uint32
	withTx(t, db, func(tx TX) {
		var err error
		_, eventID, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue, Title: "Test task", StartAt: start, EndAt: start.Add(time.Hour), Timezone: "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})
	assertLatestItemEventUser(t, db, fx.wsID, "item.scheduled", fx.userID)
	assertLatestItemEventUser(t, db, fx.wsID, "calendar.event.created", fx.userID)

	newTitle, err := taskrules.NewTitle("Agent renamed task")
	if err != nil {
		t.Fatalf("NewTitle: %v", err)
	}
	withTx(t, db, func(tx TX) {
		if _, err := RenameItemAsAgent(context.Background(), tx, RenameItemArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, NewTitle: newTitle,
		}, agentID); err != nil {
			t.Fatalf("RenameItemAsAgent: %v", err)
		}
	})
	assertLatestItemEventActor(t, db, fx.wsID, "item.renamed", agentID)
	assertLatestItemEventActor(t, db, fx.wsID, "calendar.event.updated", agentID)

	withTx(t, db, func(tx TX) {
		if err := RescheduleEventAsAgent(context.Background(), tx, RescheduleEventArgs{
			WorkspaceID: fx.wsID, EventID: eventID,
			StartAt: start.AddDate(0, 0, 7), EndAt: start.AddDate(0, 0, 7).Add(time.Hour),
		}, agentID); err != nil {
			t.Fatalf("RescheduleEventAsAgent: %v", err)
		}
	})
	assertLatestItemEventActor(t, db, fx.wsID, "item.rescheduled", agentID)
	assertLatestItemEventActor(t, db, fx.wsID, "calendar.event.updated", agentID)
}

func TestAgentSnapConfigUsesWorkspaceDefaultsInsteadOfDelegatorPreferences(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)
	if _, err := db.Exec(`UPDATE workspaces SET working_days = 'MTWTF__', timezone = 'Asia/Tokyo', country = 'JP' WHERE id = ?`, fx.wsID); err != nil {
		t.Fatalf("set workspace defaults: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET working_days = '_______', snap_to_working_day = 'auto', timezone = 'Pacific/Honolulu', country = 'US', treat_holidays_as_non_working = TRUE WHERE id = ?`, fx.userID); err != nil {
		t.Fatalf("set delegator preferences: %v", err)
	}

	var cfg SnapConfig
	if err := dbretry.InTx(context.Background(), db, "itemkit.test.agent-snap", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var err error
		cfg, err = ResolveAgentSnapConfig(ctx, tx, fx.wsID)
		return err
	}); err != nil {
		t.Fatalf("ResolveAgentSnapConfig: %v", err)
	}
	if cfg.WorkingDays != "MTWTF__" || cfg.Zone.Name() != "Asia/Tokyo" || cfg.TreatHolidays || len(cfg.Holidays) != 0 {
		t.Fatalf("agent snap config leaked delegator preferences: days=%q zone=%q holidays=%v/%v", cfg.WorkingDays, cfg.Zone.Name(), cfg.Holidays, cfg.TreatHolidays)
	}
	if cfg.Mode != region.SnapWarn {
		t.Fatalf("agent default snap mode=%q, want workspace-safe warn mode", cfg.Mode)
	}
}

func TestAgentEventActorRejectsHumanCollisionBeforeWriting(t *testing.T) {
	if _, err := agentEventActor(11, 22); err == nil {
		t.Fatal("agent event actor accepted a simultaneous legacy user id")
	}
	if _, err := agentEventActor(0, 0); err == nil {
		t.Fatal("agent event actor accepted a zero agent id")
	}
}

func seedItemAgent(t *testing.T, db *sql.DB, workspaceID uint32) uint32 {
	t.Helper()
	ctx := context.Background()
	provider, err := db.ExecContext(ctx, `INSERT INTO ai_providers (public_id, workspace_id, kind, name, api_key_ciphertext, api_key_prefix, api_key_suffix) VALUES (?, ?, 'openai_compat', 'Itemkit provider', ?, 'sk-xxxxx', 'zzzz')`, dbtype.New(), workspaceID, []byte("ciphertext"))
	if err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	providerID, err := provider.LastInsertId()
	if err != nil {
		t.Fatalf("provider id: %v", err)
	}
	model, err := db.ExecContext(ctx, `INSERT INTO ai_models (public_id, workspace_id, provider_id, name, display_name, context_window) VALUES (?, ?, ?, 'itemkit-model', 'Itemkit model', 8192)`, dbtype.New(), workspaceID, providerID)
	if err != nil {
		t.Fatalf("insert model: %v", err)
	}
	modelID, err := model.LastInsertId()
	if err != nil {
		t.Fatalf("model id: %v", err)
	}
	agent, err := db.ExecContext(ctx, `INSERT INTO ai_agents (public_id, workspace_id, model_id, name, system_prompt) VALUES (?, ?, ?, 'Itemkit Agent', 'act carefully')`, dbtype.New(), workspaceID, modelID)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	agentID, err := agent.LastInsertId()
	if err != nil {
		t.Fatalf("agent id: %v", err)
	}
	if agentID <= 0 || agentID > math.MaxUint32 {
		t.Fatalf("agent id %d does not fit uint32", agentID)
	}
	return uint32(agentID) //#nosec G115 -- the fixture checks positive uint32 bounds immediately above; testing.Fatal terminates the failing path.
}

func assertLatestItemEventActor(t *testing.T, db *sql.DB, workspaceID uint32, kind string, wantAgent uint32) {
	t.Helper()
	var userID, agentID sql.NullInt32
	if err := db.QueryRow(`SELECT actor_user_id, actor_agent_id FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, workspaceID, kind).Scan(&userID, &agentID); err != nil {
		t.Fatalf("read %s actor: %v", kind, err)
	}
	if userID.Valid || !agentID.Valid || int64(agentID.Int32) != int64(wantAgent) {
		t.Fatalf("%s actor user=%v agent=%v, want only agent %d", kind, userID, agentID, wantAgent)
	}
}

func assertLatestItemEventUser(t *testing.T, db *sql.DB, workspaceID uint32, kind string, wantUser uint32) {
	t.Helper()
	var userID, agentID sql.NullInt32
	if err := db.QueryRow(`SELECT actor_user_id, actor_agent_id FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, workspaceID, kind).Scan(&userID, &agentID); err != nil {
		t.Fatalf("read %s actor: %v", kind, err)
	}
	if !userID.Valid || int64(userID.Int32) != int64(wantUser) || agentID.Valid {
		t.Fatalf("%s actor user=%v agent=%v, want only user %d", kind, userID, agentID, wantUser)
	}
}

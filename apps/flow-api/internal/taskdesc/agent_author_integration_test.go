package taskdesc

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var taskdescDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "taskdesc_test"})

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestSnapshotWithActorKeepsAgentPublicBylineAfterAgentDeletion(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := taskdescDB.Start(ctx)
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	db := inst.DB

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	run := func(query string, args ...any) uint32 {
		t.Helper()
		res, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("fixture exec %q: %v", query, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("fixture last insert id: %v", err)
		}
		if id <= 0 || id > math.MaxUint32 {
			t.Fatalf("fixture id %d does not fit uint32", id)
		}
		return uint32(id) //#nosec G115 -- the fixture checks positive uint32 bounds immediately above; testing.Fatal terminates the failing path.
	}
	workspaceID := run(`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`, types.New(), "td-"+suffix[len(suffix)-10:], "Taskdesc "+suffix)
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = ?`, workspaceID)
	}()
	projectID := run(`INSERT INTO projects (public_id, workspace_id, name, slug, identifier) VALUES (?, ?, ?, ?, ?)`, types.New(), workspaceID, "Taskdesc", "tdp-"+suffix[len(suffix)-8:], "TD")
	taskID := run(`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title) VALUES (?, ?, ?, 1, 'Task')`, types.New(), workspaceID, projectID)
	providerID := run(`INSERT INTO ai_providers (public_id, workspace_id, kind, name, api_key_ciphertext, api_key_prefix, api_key_suffix) VALUES (?, ?, 'openai_compat', 'Taskdesc provider', ?, 'sk-xxxxx', 'zzzz')`, types.New(), workspaceID, []byte("ciphertext"))
	modelID := run(`INSERT INTO ai_models (public_id, workspace_id, provider_id, name, display_name, context_window) VALUES (?, ?, ?, 'taskdesc-model', 'Taskdesc model', 8192)`, types.New(), workspaceID, providerID)
	agentPublicID := types.New()
	agentID := run(`INSERT INTO ai_agents (public_id, workspace_id, model_id, name, system_prompt) VALUES (?, ?, ?, 'Review Agent', 'review')`, agentPublicID, workspaceID, modelID)
	if agentID > math.MaxInt32 {
		t.Fatalf("agent id %d does not fit sql.NullInt32", agentID)
	}

	var version Version
	if err := dbretry.InTx(ctx, db, "taskdesc.test.agent-snapshot", nil, func(_ context.Context, tx *dbretry.Tx) error {
		q := generated.New(tx)
		author := Actor{AgentID: sql.NullInt32{Int32: int32(agentID), Valid: true}, AgentPublicID: agentPublicID, AgentName: sql.NullString{String: "Review Agent", Valid: true}} //#nosec G115 -- the fixture rejects agent IDs above MaxInt32 before this transaction.
		var err error
		version, err = SnapshotWithActor(ctx, q, workspaceID, taskID, author, "agent authored body")
		if err != nil {
			return err
		}
		return AnnounceWithActor(ctx, tx, Announcement{
			WorkspaceID: workspaceID, TaskID: taskID, TaskPublicID: taskPublicID(ctx, t, db, taskID), Version: version,
		}, author)
	}); err != nil {
		t.Fatalf("write actor version and announcement: %v", err)
	}

	var eventUserID, eventAgentID sql.NullInt32
	if err := db.QueryRowContext(ctx, `SELECT actor_user_id, actor_agent_id FROM events WHERE workspace_id = ? AND type = 'description.version.created' ORDER BY id DESC LIMIT 1`, workspaceID).Scan(&eventUserID, &eventAgentID); err != nil {
		t.Fatalf("read announcement actor: %v", err)
	}
	if eventUserID.Valid || !eventAgentID.Valid || int64(eventAgentID.Int32) != int64(agentID) {
		t.Fatalf("announcement actor user=%v agent=%v, want only agent %d", eventUserID, eventAgentID, agentID)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id = ?`, agentID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	row, err := generated.New(db).FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{WorkspaceID: workspaceID, TaskID: taskID, PublicID: version.PublicID})
	if err != nil {
		t.Fatalf("read version after agent deletion: %v", err)
	}
	if row.AuthorAgentID.Valid || row.AuthorAgentPublicID != agentPublicID || row.AuthorAgentName != "Review Agent" {
		t.Fatalf("deleted agent byline = (%v, %v, %v), want null FK with captured public id/name", row.AuthorAgentID, row.AuthorAgentPublicID, row.AuthorAgentName)
	}
}

func taskPublicID(ctx context.Context, t *testing.T, db *sql.DB, taskID uint32) types.PublicID {
	t.Helper()
	var publicID types.PublicID
	if err := db.QueryRowContext(ctx, `SELECT public_id FROM tasks WHERE id = ?`, taskID).Scan(&publicID); err != nil {
		t.Fatalf("read task public id: %v", err)
	}
	return publicID
}

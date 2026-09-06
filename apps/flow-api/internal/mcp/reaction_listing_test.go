package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mcpReactionFixture is two workspaces the same user belongs to, each with
// one task, plus the reaction rows that decide what list_reactions may
// answer with.
type mcpReactionFixture struct {
	homeWSID  uint32
	otherWSID uint32

	userID  uint32
	userPub uuid.UUID
	peerID  uint32
	peerPub uuid.UUID

	homeTask  uuid.UUID
	otherTask uuid.UUID

	// On the home task and live: the two rows the tool must return.
	firstReaction  uuid.UUID
	secondReaction uuid.UUID
	// On the home task but soft-deleted: excluded by the enabled filter.
	disabledReaction uuid.UUID
	// On the home task, carrying the other workspace's id: excluded by the
	// workspace filter and by nothing else. reactions.workspace_id and
	// reactions.task_id are independent foreign keys, so the schema accepts
	// a row whose workspace disagrees with its task's; the WHERE clause is
	// what keeps such a row out of a tenant's answer.
	strayReaction uuid.UUID
	// On the other workspace's task: reachable only through a session bound
	// to that workspace.
	otherReaction uuid.UUID
}

func seedMCPReactionFixture(t *testing.T, db *sql.DB) *mcpReactionFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	suffix := uuid.New().String()[:8]

	insertUser := func(tag, displayName string) (uint32, uuid.UUID) {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale)
			 VALUES (?, ?, ?, 'en')`,
			pub[:], "mcprx-"+tag+"-"+suffix+"@example.test", displayName)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id), pub //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertWorkspace := func(tag string) uint32 {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
			pub[:], "mcprx-"+tag+"-"+suffix, "MCPRx "+tag)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertMember := func(wsID, userID uint32) {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
			 VALUES (?, ?, ?, 'member')`,
			pub[:], wsID, userID)
		require.NoError(t, err)
	}

	insertProject := func(wsID uint32, tag, identifier string) uint32 {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO projects (public_id, workspace_id, slug, name, identifier)
			 VALUES (?, ?, ?, ?, ?)`,
			pub[:], wsID, "mcprx-"+tag+"-"+suffix, "MCPRx "+tag, identifier)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertProjectMember := func(wsID, prjID, userID uint32) {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role)
			 VALUES (?, ?, ?, ?, 'editor')`,
			pub[:], wsID, prjID, userID)
		require.NoError(t, err)
	}

	// visibility is 'public' so the reader is gated by workspace membership
	// alone: this test is about the query's workspace binding, not about the
	// task-visibility rule that other tests cover.
	insertTask := func(wsID, prjID, creator uint32, number int, title string) (uuid.UUID, uint32) {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
			 VALUES (?, ?, ?, ?, ?, 'public', ?)`,
			pub[:], wsID, prjID, number, title, creator)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return pub, uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	// created_at is set explicitly so the ORDER BY has a single answer and
	// the assertions below can name the rows in order.
	insertReaction := func(wsID, userID, taskID uint32, emoji, createdAt string, enabled bool) uuid.UUID {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO reactions (public_id, workspace_id, user_id, task_id, emoji, created_at, enabled)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			pub[:], wsID, userID, taskID, emoji, createdAt, enabled)
		require.NoError(t, err)
		return pub
	}

	userID, userPub := insertUser("user", "Reaction Reader")
	peerID, peerPub := insertUser("peer", "Reaction Peer")

	homeWS := insertWorkspace("home")
	otherWS := insertWorkspace("other")
	insertMember(homeWS, userID)
	insertMember(homeWS, peerID)
	insertMember(otherWS, userID)

	homePrj := insertProject(homeWS, "home", "RH"+suffix[:3])
	otherPrj := insertProject(otherWS, "other", "RO"+suffix[:3])
	insertProjectMember(homeWS, homePrj, userID)
	insertProjectMember(otherWS, otherPrj, userID)

	homeTask, homeTaskID := insertTask(homeWS, homePrj, userID, 1, "Task with reactions")
	otherTask, otherTaskID := insertTask(otherWS, otherPrj, userID, 1, "Task in the other workspace")

	fx := &mcpReactionFixture{
		homeWSID:  homeWS,
		otherWSID: otherWS,
		userID:    userID,
		userPub:   userPub,
		peerID:    peerID,
		peerPub:   peerPub,
		homeTask:  homeTask,
		otherTask: otherTask,
		firstReaction: insertReaction(homeWS, userID, homeTaskID,
			"👍", "2024-03-01 09:00:01.000", true),
		secondReaction: insertReaction(homeWS, peerID, homeTaskID,
			"🎉", "2024-03-01 09:00:02.000", true),
		disabledReaction: insertReaction(homeWS, userID, homeTaskID,
			"🚀", "2024-03-01 09:00:03.000", false),
		strayReaction: insertReaction(otherWS, userID, homeTaskID,
			"🙌", "2024-03-01 09:00:04.000", true),
		otherReaction: insertReaction(otherWS, userID, otherTaskID,
			"✅", "2024-03-01 09:00:05.000", true),
	}

	require.NoError(t, tx.Commit())
	committed = true

	return fx
}

// reactionRows renders a list_reactions result back to JSON and decodes the
// items, so the assertions are on what the caller receives rather than on
// the Go value the tool happened to build.
func reactionRows(t *testing.T, out any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var payload struct {
		Reactions []map[string]any `json:"reactions"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload.Reactions
}

// requireReactionShape pins the field set of one item.
//
// Both ids the tool emits have to parse as UUIDs, and no sixth key may
// appear: the row behind this response also carries reactions.id and
// users.id, and those are internal sequential ids that never leave the
// database. An extra key is how one would reach a caller.
func requireReactionShape(t *testing.T, item map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(item))
	for k := range item {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"createdAt", "displayName", "emoji", "id", "userId"}, keys)

	for _, field := range []string{"id", "userId"} {
		s, ok := item[field].(string)
		require.Truef(t, ok, "%s is %T, want a public id string", field, item[field])
		_, err := uuid.Parse(s)
		require.NoErrorf(t, err, "%s = %q must be a public id", field, s)
	}

	createdAt, ok := item["createdAt"].(float64)
	require.Truef(t, ok, "createdAt is %T, want a number", item["createdAt"])
	require.Positive(t, createdAt)
}

// TestMCPListReactionsReturnsTheTaskRows drives list_reactions against a task
// that carries reactions and asserts the rows come back.
//
// The statement behind the tool filters on workspace_id as well as task_id.
// Leaving that parameter unset is neither a compile error nor a runtime one:
// it binds zero, no row carries workspace zero, and the tool answers every
// task with an empty list. An assertion that the tool returns without error,
// or that it excludes rows it should exclude, agrees with that answer — so
// the load-bearing assertions here are on rows that must be present.
func TestMCPListReactionsReturnsTheTaskRows(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPReactionFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	home := mcp.NewTestSession(fx.userID, fx.homeWSID, []string{"read:workspace", "write:workspace"})
	other := mcp.NewTestSession(fx.userID, fx.otherWSID, []string{"read:workspace", "write:workspace"})

	homeArgs := mcpVisJSON(t, map[string]any{"taskId": fx.homeTask.String()})
	otherArgs := mcpVisJSON(t, map[string]any{"taskId": fx.otherTask.String()})

	t.Run("live rows come back in created order", func(t *testing.T) {
		out, err := mcp.RunListReactions(ctx, deps, home, homeArgs)
		require.NoError(t, err)
		items := reactionRows(t, out)
		require.Len(t, items, 2, "the task carries two live reactions in this workspace")

		requireReactionShape(t, items[0])
		require.Equal(t, fx.firstReaction.String(), items[0]["id"])
		require.Equal(t, "👍", items[0]["emoji"])
		require.Equal(t, fx.userPub.String(), items[0]["userId"])
		require.Equal(t, "Reaction Reader", items[0]["displayName"])

		requireReactionShape(t, items[1])
		require.Equal(t, fx.secondReaction.String(), items[1]["id"])
		require.Equal(t, "🎉", items[1]["emoji"])
		require.Equal(t, fx.peerPub.String(), items[1]["userId"])
		require.Equal(t, "Reaction Peer", items[1]["displayName"])

		require.Less(t, items[0]["createdAt"].(float64), items[1]["createdAt"].(float64))
	})

	t.Run("a soft-deleted reaction stays out", func(t *testing.T) {
		out, err := mcp.RunListReactions(ctx, deps, home, homeArgs)
		require.NoError(t, err)
		for _, item := range reactionRows(t, out) {
			require.NotEqual(t, fx.disabledReaction.String(), item["id"],
				"a reaction whose enabled flag is off has been withdrawn")
		}
	})

	t.Run("another workspace's rows stay out", func(t *testing.T) {
		// Two rows the home listing must not contain: one attached to the
		// home task under the other workspace's id, which only the
		// workspace filter excludes, and one belonging to the other
		// workspace's task.
		out, err := mcp.RunListReactions(ctx, deps, home, homeArgs)
		require.NoError(t, err)
		for _, item := range reactionRows(t, out) {
			require.NotEqual(t, fx.strayReaction.String(), item["id"],
				"this row names the home task but the other workspace")
			require.NotEqual(t, fx.otherReaction.String(), item["id"])
		}

		// The other workspace is not empty either, so the exclusion above is
		// a filter doing its work rather than an answer that is empty
		// everywhere.
		otherOut, err := mcp.RunListReactions(ctx, deps, other, otherArgs)
		require.NoError(t, err)
		otherItems := reactionRows(t, otherOut)
		require.Len(t, otherItems, 1)
		require.Equal(t, fx.otherReaction.String(), otherItems[0]["id"])
		require.Equal(t, "✅", otherItems[0]["emoji"])
	})

	t.Run("a task in another workspace is not found", func(t *testing.T) {
		_, err := mcp.RunListReactions(ctx, deps, home, otherArgs)
		requireTaskNotFound(t, err)
	})

	t.Run("a reaction added through the tool is listed by it", func(t *testing.T) {
		added, err := mcp.RunAddReaction(ctx, deps, home, mcpVisJSON(t, map[string]any{
			"taskId": fx.homeTask.String(),
			"emoji":  "🔥",
		}))
		require.NoError(t, err)
		result, ok := added.(map[string]any)
		require.True(t, ok)
		addedID, ok := result["id"].(string)
		require.True(t, ok)

		out, err := mcp.RunListReactions(ctx, deps, home, homeArgs)
		require.NoError(t, err)
		items := reactionRows(t, out)
		require.Len(t, items, 3)
		// Newest last: the seeded rows carry fixed, earlier timestamps.
		require.Equal(t, addedID, items[2]["id"])
		require.Equal(t, "🔥", items[2]["emoji"])
		require.Equal(t, fx.userPub.String(), items[2]["userId"])
	})
}

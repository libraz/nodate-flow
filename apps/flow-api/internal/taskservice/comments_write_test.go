package taskservice_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

type commentKit struct {
	t   *testing.T
	db  *sql.DB
	ctx context.Context
	f   serviceFixture
}

func newCommentKit(t *testing.T, withAgent bool) commentKit {
	t.Helper()
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return commentKit{t: t, db: db, ctx: ctx, f: seedServiceFixture(t, db, withAgent)}
}

func (k commentKit) inTx(fn func(*dbretry.Tx) error) error {
	return dbretry.InTx(k.ctx, k.db, "taskservice.comment.test", nil, func(_ context.Context, tx *dbretry.Tx) error { return fn(tx) })
}

func (k commentKit) userActor() mutationlog.Actor {
	return mutationlog.Actor{UserID: k.f.userID, WorkspaceID: k.f.workspaceID}
}

func (k commentKit) userArgs(body string) taskservice.WriteArgs {
	return taskservice.WriteArgs{
		Actor:        k.userActor(),
		TaskID:       k.f.taskID,
		TaskPublicID: k.f.taskPublicID,
		AuthorUserID: sql.NullInt64{Int64: int64(k.f.userID), Valid: true},
		Body:         body,
	}
}

func (k commentKit) write(args taskservice.WriteArgs) (taskservice.WrittenComment, error) {
	var out taskservice.WrittenComment
	err := k.inTx(func(tx *dbretry.Tx) error {
		var err error
		out, err = taskservice.WriteComment(k.ctx, tx, args)
		return err
	})
	return out, err
}

func (k commentKit) eventCount(kind string, comment types.PublicID) int {
	var n int
	require.NoError(k.t, k.db.QueryRowContext(k.ctx, `SELECT COUNT(*) FROM events
WHERE workspace_id = ? AND task_id = ? AND type = ? AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.commentId')) = ?`,
		k.f.workspaceID, k.f.taskID, kind, comment.String()).Scan(&n))
	return n
}

func (k commentKit) mentionCount(commentID uint32) int {
	var n int
	require.NoError(k.t, k.db.QueryRowContext(k.ctx,
		`SELECT COUNT(*) FROM mentions WHERE workspace_id = ? AND comment_id = ?`, k.f.workspaceID, commentID).Scan(&n))
	return n
}

func (k commentKit) mentionBody(text string) string {
	return fmt.Sprintf("%s @[Human](user:%s)", text, k.f.userPublicID.String())
}

func TestWriteComment(t *testing.T) {
	k := newCommentKit(t, true)

	// Zero CreatedAt and SourceUID keep the current behaviour.
	before := time.Now().Add(-time.Minute)
	written, err := k.write(k.userArgs(k.mentionBody("Live")))
	require.NoError(t, err)
	require.NotZero(t, written.ID)
	require.Equal(t, k.f.taskPublicID.String(), written.Payload["taskId"])
	require.Equal(t, written.PublicID.String(), written.Payload["commentId"])
	var (
		createdAt time.Time
		sourceUID sql.NullString
		authorID  sql.NullInt32
		kind      string
	)
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT created_at, source_uid, author_id, author_kind FROM comments WHERE id = ?`, written.ID).
		Scan(&createdAt, &sourceUID, &authorID, &kind))
	require.True(t, createdAt.After(before))
	require.False(t, sourceUID.Valid)
	require.Equal(t, int64(k.f.userID), int64(authorID.Int32))
	require.Equal(t, taskservice.AuthorKindUser, kind)
	require.Equal(t, 1, k.eventCount("task.comment.added", written.PublicID))
	require.Equal(t, 1, k.mentionCount(written.ID))

	var actorUser, actorAgent sql.NullInt32
	var payload []byte
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT actor_user_id, actor_agent_id, payload_json FROM events
WHERE workspace_id = ? AND type = 'task.comment.added' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.commentId')) = ?`,
		k.f.workspaceID, written.PublicID.String()).Scan(&actorUser, &actorAgent, &payload))
	require.Equal(t, int64(k.f.userID), int64(actorUser.Int32))
	require.False(t, actorAgent.Valid)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, map[string]any{"taskId": k.f.taskPublicID.String(), "commentId": written.PublicID.String()}, decoded)

	// An importer write stores the explicit time and source uid.
	at := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	imported := k.userArgs("Imported")
	imported.CreatedAt = at
	imported.SourceUID = "asana:1201"
	imported.Payload = map[string]any{"source": "asana"}
	row, err := k.write(imported)
	require.NoError(t, err)
	require.Equal(t, "asana", row.Payload["source"])
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT created_at, source_uid FROM comments WHERE id = ?`, row.ID).Scan(&createdAt, &sourceUID))
	require.True(t, createdAt.Equal(at), "created_at %s, want %s", createdAt, at)
	require.Equal(t, "asana:1201", sourceUID.String)

	// The same source uid on the same task is refused without an event.
	_, err = k.write(imported)
	require.True(t, errors.Is(err, taskservice.ErrCommentSourceTaken), "got %v", err)
	var added int
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND type = 'task.comment.added'`,
		k.f.workspaceID, k.f.taskID).Scan(&added))
	require.Equal(t, 2, added)

	// An agent author takes the byline snapshot and the agent attribution.
	agentArgs := taskservice.WriteArgs{
		Actor:         mutationlog.Actor{AgentID: k.f.agentID, WorkspaceID: k.f.workspaceID},
		TaskID:        k.f.taskID,
		TaskPublicID:  k.f.taskPublicID,
		AuthorAgentID: sql.NullInt64{Int64: int64(k.f.agentID), Valid: true},
		Body:          k.mentionBody("Agent"),
	}
	agentRow, err := k.write(agentArgs)
	require.NoError(t, err)
	var agentPub, agentName string
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT author_kind, author_display_name, author_agent_public_id
FROM v_comment_for_task WHERE workspace_id = ? AND public_id = ?`, k.f.workspaceID, agentRow.PublicID).Scan(&kind, &agentName, &agentPub))
	require.Equal(t, taskservice.AuthorKindAgent, kind)
	require.Equal(t, k.f.agentName, agentName)
	require.Equal(t, k.f.agentPublicID.String(), agentPub)
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT actor_user_id, actor_agent_id FROM events
WHERE workspace_id = ? AND type = 'task.comment.added' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.commentId')) = ?`,
		k.f.workspaceID, agentRow.PublicID.String()).Scan(&actorUser, &actorAgent))
	require.False(t, actorUser.Valid)
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	require.Equal(t, 0, k.mentionCount(agentRow.ID))

	// Refusals write nothing.
	_, err = k.write(k.userArgs(""))
	require.True(t, errors.Is(err, taskservice.ErrCommentLength))
	both := k.userArgs("Both")
	both.AuthorAgentID = sql.NullInt64{Int64: int64(k.f.agentID), Valid: true}
	_, err = k.write(both)
	require.True(t, errors.Is(err, taskservice.ErrCommentAuthor))
	_, err = k.db.ExecContext(k.ctx, `UPDATE ai_agents SET enabled = FALSE WHERE id = ?`, k.f.agentID)
	require.NoError(t, err)
	_, err = k.write(agentArgs)
	require.True(t, errors.Is(err, taskservice.ErrCommentAgentUnavailable))
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT COUNT(*) FROM comments WHERE workspace_id = ? AND task_id = ?`,
		k.f.workspaceID, k.f.taskID).Scan(&added))
	require.Equal(t, 3, added)
}

func TestEditComment(t *testing.T) {
	k := newCommentKit(t, false)
	written, err := k.write(k.userArgs("Plain"))
	require.NoError(t, err)
	require.Equal(t, 0, k.mentionCount(written.ID))

	args := taskservice.EditArgs{
		Actor:           k.userActor(),
		CommentID:       written.ID,
		CommentPublicID: written.PublicID,
		TaskID:          k.f.taskID,
		TaskPublicID:    k.f.taskPublicID,
		AuthorUserID:    k.f.userID,
		Body:            k.mentionBody("Edited"),
	}
	require.NoError(t, k.inTx(func(tx *dbretry.Tx) error { return taskservice.EditComment(k.ctx, tx, args) }))
	var body string
	var editedAt sql.NullTime
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT body, edited_at FROM comments WHERE id = ?`, written.ID).Scan(&body, &editedAt))
	require.Equal(t, args.Body, body)
	require.True(t, editedAt.Valid)
	require.Equal(t, 1, k.eventCount("task.comment.edited", written.PublicID))
	require.Equal(t, 1, k.mentionCount(written.ID))

	// Re-submitting the same body is not an error and still records the edit.
	require.NoError(t, k.inTx(func(tx *dbretry.Tx) error { return taskservice.EditComment(k.ctx, tx, args) }))
	require.Equal(t, 2, k.eventCount("task.comment.edited", written.PublicID))

	// Dropping the mention clears it.
	args.Body = "No one named"
	require.NoError(t, k.inTx(func(tx *dbretry.Tx) error { return taskservice.EditComment(k.ctx, tx, args) }))
	require.Equal(t, 0, k.mentionCount(written.ID))

	args.Body = ""
	err = k.inTx(func(tx *dbretry.Tx) error { return taskservice.EditComment(k.ctx, tx, args) })
	require.True(t, errors.Is(err, taskservice.ErrCommentLength))
	require.Equal(t, 3, k.eventCount("task.comment.edited", written.PublicID))
}

func TestDisableComment(t *testing.T) {
	k := newCommentKit(t, false)
	written, err := k.write(k.userArgs(k.mentionBody("Doomed")))
	require.NoError(t, err)
	require.Equal(t, 1, k.mentionCount(written.ID))

	args := taskservice.DisableArgs{
		Actor:           k.userActor(),
		CommentID:       written.ID,
		CommentPublicID: written.PublicID,
		TaskID:          k.f.taskID,
		TaskPublicID:    k.f.taskPublicID,
	}

	// Another workspace cannot reach the row.
	other := args
	other.Actor.WorkspaceID = k.f.workspaceID + 1_000_000
	err = k.inTx(func(tx *dbretry.Tx) error { return taskservice.DisableComment(k.ctx, tx, other) })
	require.True(t, errors.Is(err, taskservice.ErrCommentNotFound))

	require.NoError(t, k.inTx(func(tx *dbretry.Tx) error { return taskservice.DisableComment(k.ctx, tx, args) }))
	var enabled bool
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT enabled FROM comments WHERE id = ?`, written.ID).Scan(&enabled))
	require.False(t, enabled)
	require.Equal(t, 0, k.mentionCount(written.ID))
	require.Equal(t, 1, k.eventCount("task.comment.removed", written.PublicID))

	err = k.inTx(func(tx *dbretry.Tx) error { return taskservice.DisableComment(k.ctx, tx, args) })
	require.True(t, errors.Is(err, taskservice.ErrCommentNotFound))
	require.Equal(t, 1, k.eventCount("task.comment.removed", written.PublicID), "a refused delete appends nothing")

	// The source uid of a disabled comment no longer blocks a re-import.
	imported := k.userArgs("Imported")
	imported.SourceUID = "asana:77"
	first, err := k.write(imported)
	require.NoError(t, err)
	require.NoError(t, k.inTx(func(tx *dbretry.Tx) error {
		return taskservice.DisableComment(k.ctx, tx, taskservice.DisableArgs{
			Actor: k.userActor(), CommentID: first.ID, CommentPublicID: first.PublicID,
			TaskID: k.f.taskID, TaskPublicID: k.f.taskPublicID,
		})
	}))
	_, err = k.write(imported)
	require.NoError(t, err)
}

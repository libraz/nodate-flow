package labelkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "labelkit_test",
})

type fixtures struct {
	wsID, userID, projectID, taskID uint32
}

func seed(ctx context.Context, t *testing.T, db *sql.DB) fixtures {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slugPart := suffix[len(suffix)-10:]
	exec := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}
	wsID := exec(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		dbtype.New(), "ws-"+slugPart, "LabelKit Test "+suffix)
	userID := exec(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "labelkit+"+suffix+"@example.test", "LabelKit Tester")
	exec(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at) VALUES (?, ?, ?, 'owner', NOW())`,
		dbtype.New(), wsID, userID)
	projectID := exec(`INSERT INTO projects (public_id, workspace_id, name, slug, identifier) VALUES (?, ?, ?, ?, ?)`,
		dbtype.New(), wsID, "LabelKit Project", "pj-"+slugPart, "LKT")
	taskID := exec(`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility) VALUES (?, ?, ?, 1, 'Test task', 'public')`,
		dbtype.New(), wsID, projectID)
	return fixtures{wsID: wsID, userID: userID, projectID: projectID, taskID: taskID}
}

func purge(t *testing.T, db *sql.DB, wsID uint32) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Logf("purge: acquire conn: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, q := range []string{
		`DELETE FROM events WHERE workspace_id = ?`,
		`DELETE FROM task_labels WHERE workspace_id = ?`,
		`DELETE FROM labels WHERE workspace_id = ?`,
		`DELETE FROM tasks WHERE workspace_id = ?`,
		`DELETE FROM projects WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := conn.ExecContext(ctx, q, wsID); err != nil {
			t.Logf("purge %q: %v", q, err)
		}
	}
	_, _ = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
}

func inTx(ctx context.Context, db *sql.DB, fn func(tx *dbretry.Tx) error) error {
	return dbretry.InTx(ctx, db, "labelkit.test", nil,
		func(_ context.Context, tx *dbretry.Tx) error { return fn(tx) })
}

func TestLabelKit(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := shared.Start(ctx)
	require.NoError(t, err)
	db := inst.DB

	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)
	actor := mutationlog.Actor{UserID: fx.userID, WorkspaceID: fx.wsID}

	eventCount := func(kind string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`, fx.wsID, kind).Scan(&n))
		return n
	}
	create := func(name string) (Label, error) {
		var l Label
		err := inTx(ctx, db, func(tx *dbretry.Tx) error {
			var e error
			l, e = Create(ctx, tx, CreateArgs{WorkspaceID: fx.wsID, Name: name, Color: "#112233", Actor: actor})
			return e
		})
		return l, err
	}

	var label Label

	t.Run("Create", func(t *testing.T) {
		l, err := create("bug")
		require.NoError(t, err)
		assert.NotZero(t, l.ID)
		assert.NotEqual(t, dbtype.PublicID{}, l.PublicID)
		label = l
		assert.Equal(t, 1, eventCount("label.created"))
	})

	t.Run("Create/ErrNameTaken", func(t *testing.T) {
		_, err := create("bug")
		assert.ErrorIs(t, err, ErrNameTaken)
		assert.Equal(t, 1, eventCount("label.created"))
	})

	attach := func() error {
		return inTx(ctx, db, func(tx *dbretry.Tx) error {
			return Attach(ctx, tx, AttachArgs{TaskID: fx.taskID, LabelID: label.ID, LabelPublicID: label.PublicID, Actor: actor})
		})
	}
	detach := func() error {
		return inTx(ctx, db, func(tx *dbretry.Tx) error {
			return Detach(ctx, tx, AttachArgs{TaskID: fx.taskID, LabelID: label.ID, LabelPublicID: label.PublicID, Actor: actor})
		})
	}

	t.Run("Attach", func(t *testing.T) {
		require.NoError(t, attach())
		assert.Equal(t, 1, eventCount("task.label.added"))
	})

	t.Run("Attach/ErrAlreadyAttached", func(t *testing.T) {
		assert.True(t, errors.Is(attach(), ErrAlreadyAttached))
		assert.Equal(t, 1, eventCount("task.label.added"))
	})

	t.Run("Detach", func(t *testing.T) {
		require.NoError(t, detach())
		assert.Equal(t, 1, eventCount("task.label.removed"))
	})

	t.Run("Detach/ErrNotAttached", func(t *testing.T) {
		assert.ErrorIs(t, detach(), ErrNotAttached)
		assert.Equal(t, 1, eventCount("task.label.removed"))
	})

	t.Run("Attach/AfterDetach", func(t *testing.T) {
		require.NoError(t, attach())
		assert.Equal(t, 2, eventCount("task.label.added"))
	})
}

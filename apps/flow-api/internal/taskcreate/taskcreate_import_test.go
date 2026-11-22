package taskcreate_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

func TestNewHonoursCreatedAtAndSourceUID(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err)
	db := inst.DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	wsPub := types.New()
	execID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- fixture ids are small positive values
	}
	wsID := execID(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`, wsPub, "tc-"+suffix[len(suffix)-12:], "Create "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	projectID := execID(`INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`, types.New(), wsID, "P", "tc-"+suffix[len(suffix)-12:])

	create := func(title string, mutate func(*taskcreate.Args)) taskcreate.Result {
		tt, err := taskrules.NewTitle(title)
		require.NoError(t, err)
		args := taskcreate.Args{WorkspaceID: wsID, ProjectID: projectID, Title: tt}
		mutate(&args)
		var res taskcreate.Result
		require.NoError(t, dbretry.InTx(ctx, db, "taskcreate.test", nil, func(_ context.Context, tx *dbretry.Tx) error {
			var err error
			res, err = taskcreate.New(ctx, tx, taskcreate.Unattributed(), args)
			return err
		}))
		return res
	}
	read := func(id int64) (time.Time, sql.NullString) {
		var created time.Time
		var uid sql.NullString
		require.NoError(t, db.QueryRowContext(ctx, `SELECT created_at, source_uid FROM tasks WHERE id = ?`, id).Scan(&created, &uid))
		return created, uid
	}

	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	imported := create("Imported", func(a *taskcreate.Args) {
		a.CreatedAt = want
		a.SourceUID = "asana:12345"
	})
	created, uid := read(imported.ID)
	require.True(t, want.Equal(created.UTC()), "created_at %s, want %s", created, want)
	require.Equal(t, sql.NullString{String: "asana:12345", Valid: true}, uid)

	before := time.Now().UTC().Add(-time.Minute)
	plain := create("Plain", func(*taskcreate.Args) {})
	created, uid = read(plain.ID)
	require.WithinDuration(t, time.Now().UTC(), created.UTC(), 5*time.Minute)
	require.True(t, created.UTC().After(before))
	require.False(t, uid.Valid, "source_uid stays NULL when not given")
}

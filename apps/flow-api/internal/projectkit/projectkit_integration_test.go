package projectkit_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/projectkit"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type fixture struct {
	workspaceID uint32
	leadID      uint32
	otherID     uint32
}

func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	require.NotNil(t, inst)
	return inst.DB
}

func seed(t *testing.T, db *sql.DB) fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	wsPub := types.New()
	execID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec %q", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- fixture ids are small positive values
	}
	var f fixture
	f.workspaceID = execID(`INSERT INTO workspaces (public_id, slug, name, timezone)
VALUES (?, ?, ?, 'UTC')`, wsPub, "pk-"+suffix, "Projectkit "+suffix)
	f.leadID = execID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, types.New(), "pklead+"+suffix+"@example.test", "Lead "+suffix)
	f.otherID = execID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, types.New(), "pkother+"+suffix+"@example.test", "Other "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	return f
}

func inTx(t *testing.T, db *sql.DB, fn func(ctx context.Context, tx *dbretry.Tx) error) error {
	t.Helper()
	return dbretry.InTx(context.Background(), db, "projectkit.test", nil, fn)
}

func TestProjectKit(t *testing.T) {
	db := integrationDB(t)
	f := seed(t, db)
	q := generated.New(db)
	ctx := context.Background()

	args := func(slug, identifier string) projectkit.CreateArgs {
		return projectkit.CreateArgs{
			WorkspaceID: f.workspaceID,
			Slug:        slug,
			Identifier:  sql.NullString{String: identifier, Valid: identifier != ""},
			Name:        "Project " + slug,
			LeadUserID:  f.leadID,
		}
	}
	create := func(a projectkit.CreateArgs) (projectkit.Project, error) {
		var p projectkit.Project
		err := inTx(t, db, func(ctx context.Context, tx *dbretry.Tx) error {
			var err error
			p, err = projectkit.Create(ctx, tx, a)
			return err
		})
		return p, err
	}

	t.Run("create with lead", func(t *testing.T) {
		p, err := create(args("alpha", "ALP"))
		require.NoError(t, err)
		require.NotZero(t, p.ID)
		m, err := q.FindProjectMemberByUserId(ctx, generated.FindProjectMemberByUserIdParams{ProjectID: p.ID, UserID: f.leadID})
		require.NoError(t, err)
		require.Equal(t, generated.ProjectMembersRoleLead, m.Role)
	})

	t.Run("slug taken", func(t *testing.T) {
		_, err := create(args("beta", "BET"))
		require.NoError(t, err)
		_, err = create(args("beta", "BE2"))
		require.ErrorIs(t, err, projectkit.ErrSlugTaken)
	})

	t.Run("identifier taken", func(t *testing.T) {
		_, err := create(args("gamma", "GAM"))
		require.NoError(t, err)
		_, err = create(args("gamma2", "GAM"))
		require.ErrorIs(t, err, projectkit.ErrIdentifierTaken)
	})

	t.Run("add member idempotent", func(t *testing.T) {
		p, err := create(args("delta", "DEL"))
		require.NoError(t, err)
		add := func(role generated.ProjectMembersRole) bool {
			var added bool
			require.NoError(t, inTx(t, db, func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				added, err = projectkit.AddMember(ctx, tx, projectkit.MemberArgs{
					WorkspaceID: f.workspaceID,
					ProjectID:   p.ID,
					UserID:      f.otherID,
					Role:        role,
					PublicID:    types.New(),
					AddedAt:     time.Now(),
				})
				return err
			}))
			return added
		}
		require.True(t, add(generated.ProjectMembersRoleEditor))
		require.False(t, add(generated.ProjectMembersRoleCommenter))
		m, err := q.FindProjectMemberByUserId(ctx, generated.FindProjectMemberByUserIdParams{ProjectID: p.ID, UserID: f.otherID})
		require.NoError(t, err)
		require.Equal(t, generated.ProjectMembersRoleEditor, m.Role)
	})
}

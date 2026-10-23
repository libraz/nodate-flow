package workspace

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// slugRaceDB is a generated.DBTX decorator that lets a competing workspace
// take the slug right after the handler's pre-check has found it free, so
// the handler's own insert is the one uniq_workspaces_slug rejects.
type slugRaceDB struct {
	*sql.DB
	once    sync.Once
	compete func(ctx context.Context)
}

// QueryRowContext fires the competing create once, after the slug
// pre-check has been answered.
func (d *slugRaceDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	row := d.DB.QueryRowContext(ctx, query, args...)
	if strings.Contains(query, "FROM workspaces") && strings.Contains(query, "slug = ?") {
		d.once.Do(func() { d.compete(ctx) })
	}
	return row
}

// TestCreateWorkspace_LostSlugRaceIsAConflict holds the loser of a slug
// race to the answer the pre-check gives: the slug is taken, never 500.
func TestCreateWorkspace_LostSlugRaceIsAConflict(t *testing.T) {
	inst := requireWorkspaceTxDB(t)
	ctx := context.Background()
	uid := raceNewUser(t, generated.New(inst.DB))
	slug := "race-" + types.New().String()[:8]

	raceDB := &slugRaceDB{DB: inst.DB, compete: func(ctx context.Context) {
		_, err := inst.DB.ExecContext(ctx,
			`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, 'Winner', 'UTC')`,
			types.New(), slug)
		require.NoError(t, err)
	}}
	deps := Deps{DB: inst.DB, Queries: generated.New(raceDB), Audit: audit.NoopSink{}}

	_, err := Create(deps)(authn.WithActor(ctx, uid), &CreateWorkspaceInput{
		Body: CreateWorkspaceInputBody{Slug: slug, Name: "Loser", Timezone: "UTC", Country: "US"},
	})

	var problem *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &problem)
	require.Equal(t, "WS.WORKSPACE.SLUG_ALREADY_TAKEN", problem.Type)
}

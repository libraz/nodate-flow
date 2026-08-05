package workspace

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// inviteRaceDB lazily boots a shared MySQL testcontainer with the full
// repo schema. The invite max_uses TOCTOU regression needs a real DB
// because it races N concurrent redemptions against the atomic
// conditional UPDATE (IncrementInviteUseCount) and asserts exactly
// max_uses members are admitted.
var inviteRaceDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_invite_race_test"})

// requireInviteRaceDB skips when integration tests are not enabled and
// otherwise returns the shared *sql.DB.
func requireInviteRaceDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping invite race integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run invite race integration tests")
	}
	inst, err := inviteRaceDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst.DB
}

// raceNewUser inserts a fresh user + local identity and returns the
// internal user id. Each call uses a unique email so the racing
// redemptions all come from distinct, not-yet-member users.
func raceNewUser(t *testing.T, q *generated.Queries) uint32 {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	email := "invite-race-" + pub.String() + "@example.test"
	uid64, err := q.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        pub,
		Email:           email,
		DisplayName:     "Invite Race User",
		Locale:          "en",
		Timezone:        "UTC",
		Country:         sql.NullString{String: "US", Valid: true},
		ThemePreference: generated.UsersThemePreferenceSystem,
	})
	require.NoError(t, err)
	uid := uint32(uid64) //#nosec G115 -- users.id is BIGINT UNSIGNED, fits uint32 in test fixtures
	_, err = q.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID:     types.New(),
		UserID:       uid,
		Provider:     generated.IdentitiesProviderLocal,
		Subject:      email,
		PasswordHash: sql.NullString{},
	})
	require.NoError(t, err)
	return uid
}

// raceWorkspace inserts a fresh workspace and returns its internal id.
func raceWorkspace(t *testing.T, q *generated.Queries) uint32 {
	t.Helper()
	ctx := context.Background()
	pub := types.New()
	id64, err := q.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
		PublicID: pub,
		Slug:     "invite-race-" + pub.String(),
		Name:     "Invite Race Workspace",
		Timezone: "UTC",
		Country:  sql.NullString{String: "US", Valid: true},
	})
	require.NoError(t, err)
	return uint32(id64) //#nosec G115 -- workspaces.id is INT UNSIGNED, fits uint32
}

// raceInvite creates a workspace invite capped at maxUses and returns
// the plaintext token clients would redeem.
func raceInvite(t *testing.T, q *generated.Queries, workspaceID uint32, maxUses int32) string {
	t.Helper()
	ctx := context.Background()
	plaintext, hash, err := auth.GenerateOpaque(PrefixInvite)
	require.NoError(t, err)
	_, err = q.CreateWorkspaceInvite(ctx, generated.CreateWorkspaceInviteParams{
		PublicID:    types.New(),
		WorkspaceID: workspaceID,
		TokenHash:   hash,
		Role:        generated.WorkspaceInvitesRole("member"),
		MaxUses:     sql.NullInt32{Int32: maxUses, Valid: true},
	})
	require.NoError(t, err)
	return plaintext
}

// TestAcceptInvite_ConcurrentRedemptions_RespectMaxUses proves the
// max_uses cap holds under concurrency. N distinct, not-yet-member users
// race POST /invites/{token}/accept against a single invite with
// max_uses = k. The atomic conditional increment must admit exactly k
// members; every loser must receive WORKSPACE_INVITE.EXHAUSTED and never
// a silent over-the-cap join. Before the fix the non-transactional
// pre-check let all N readers observe use_count < max_uses and the blind
// increment admitted all N — the classic TOCTOU overrun this test pins.
func TestAcceptInvite_ConcurrentRedemptions_RespectMaxUses(t *testing.T) {
	db := requireInviteRaceDB(t)
	q := generated.New(db)
	ctx := context.Background()

	const (
		racers  = 8
		maxUses = 3
	)

	workspaceID := raceWorkspace(t, q)
	token := raceInvite(t, q, workspaceID, maxUses)

	uids := make([]uint32, racers)
	for i := range uids {
		uids[i] = raceNewUser(t, q)
	}

	deps := InviteDeps{Deps: Deps{DB: db, Queries: q, Audit: audit.NoopSink{}}}
	handler := AcceptInvite(deps)

	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		okCount       int
		exhaustCount  int
		unexpectedErr error
		start         = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(uid uint32) {
			defer wg.Done()
			actorCtx := authn.WithActor(ctx, uid)
			<-start // release all goroutines simultaneously
			_, herr := handler(actorCtx, &AcceptInviteInput{Token: token})
			mu.Lock()
			defer mu.Unlock()
			if herr == nil {
				okCount++
				return
			}
			var problem *handlerutil.ProblemDetails
			if assertExhausted(herr, &problem) {
				exhaustCount++
			} else {
				unexpectedErr = herr
			}
		}(uids[i])
	}
	close(start)
	wg.Wait()

	require.NoError(t, unexpectedErr, "losers must fail with EXHAUSTED, not an unrelated error")
	assert.Equal(t, maxUses, okCount, "exactly max_uses concurrent redemptions may succeed")
	assert.Equal(t, racers-maxUses, exhaustCount, "every over-the-cap redemption must be told the invite is exhausted")

	// Authoritative DB check: use_count must equal max_uses, never more.
	invite, err := q.FindWorkspaceInviteByTokenHash(ctx, auth.HashOpaque(token))
	require.NoError(t, err)
	assert.Equal(t, int64(maxUses), int64(invite.UseCount), "use_count must never exceed max_uses after the race")

	// And exactly max_uses members must have been admitted to the workspace.
	var memberCount int64
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND enabled = TRUE",
		workspaceID,
	).Scan(&memberCount))
	assert.Equal(t, int64(maxUses), memberCount, "exactly max_uses members may be admitted")
}

// assertExhausted reports whether err is a ProblemDetails carrying the
// WORKSPACE_INVITE.EXHAUSTED code.
func assertExhausted(err error, problem **handlerutil.ProblemDetails) bool {
	if !errors.As(err, problem) {
		return false
	}
	return (*problem).Type == apierrors.WsWorkspaceInviteExhausted.Code
}

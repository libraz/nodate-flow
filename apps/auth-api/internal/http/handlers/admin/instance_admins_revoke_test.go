package admin

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// recordingSink captures every audit entry a handler emits so a test can
// assert on both what was written and that nothing was.
type recordingSink struct {
	mu      sync.Mutex
	entries []audit.Entry
}

// Record appends the entry to the captured slice.
func (s *recordingSink) Record(_ context.Context, e audit.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

// actions returns the action string of every captured entry.
func (s *recordingSink) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.Action
	}
	return out
}

// revokeRaceDB is a generated.DBTX decorator that opens the window between
// the revoke handler's existence check and its transaction: right after the
// check reads the grant as active, a competing revocation runs to
// completion, so the handler's own statement matches no row. Embedding
// *sql.DB leaves every other DBTX method untouched.
type revokeRaceDB struct {
	*sql.DB
	once    sync.Once
	compete func(ctx context.Context)
}

// QueryRowContext fires the competing revocation once, immediately after
// the handler's existence check has been answered.
func (d *revokeRaceDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	row := d.DB.QueryRowContext(ctx, query, args...)
	if strings.Contains(query, "FROM instance_admins") && strings.Contains(query, "WHERE user_id = ?") {
		d.once.Do(func() { d.compete(ctx) })
	}
	return row
}

// revokeFixture resets instance_admins and grants admin to two fresh
// users, returning the actor and the revoke target. Two grants are needed
// so the last-admin guard does not short-circuit the handler before it
// reaches the write.
func revokeFixture(t *testing.T, db *sql.DB, q *generated.Queries) (actorID, targetID uint32, targetPub types.PublicID) {
	t.Helper()
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)

	actorID = setupNewUser(t, q)
	targetID = setupNewUser(t, q)

	for _, uid := range []uint32{actorID, targetID} {
		_, err = q.AdminGrantInstanceAdmin(ctx, generated.AdminGrantInstanceAdminParams{
			PublicID:        types.New(),
			UserID:          uid,
			GrantedByUserID: sql.NullInt32{},
		})
		require.NoError(t, err)
	}

	require.NoError(t, db.QueryRowContext(ctx, "SELECT public_id FROM users WHERE id = ?", targetID).Scan(&targetPub))
	return actorID, targetID, targetPub
}

// TestRevokeAdmin_LosesTheRace_IsNotFoundAndUnaudited pins the outcome of
// the window between the handler's existence check and its write: when
// another path revokes the grant first, the handler's UPDATE matches no
// row. It must answer not-found and leave the audit trail empty, because
// an entry here would describe a revocation that never happened.
func TestRevokeAdmin_LosesTheRace_IsNotFoundAndUnaudited(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID, targetID, targetPub := revokeFixture(t, db, q)

	raceDB := &revokeRaceDB{DB: db, compete: func(context.Context) {
		affected, err := q.AdminRevokeInstanceAdmin(ctx, targetID)
		require.NoError(t, err)
		require.Equal(t, int64(1), affected, "the competing revocation must be the one that wins")
	}}

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: generated.New(raceDB), Audit: sink}

	_, err := RevokeAdmin(deps)(authn.WithActor(ctx, actorID), &RevokeAdminInput{UserID: targetPub.String()})

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob, "a revocation that changed nothing must be reported as an error")
	assert.Equal(t, "INSTANCE.ADMIN.NOT_FOUND", prob.Type,
		"a grant that is already gone reads the same as one that was never there")
	assert.Empty(t, sink.actions(), "no audit entry may claim a revocation this call did not perform")
}

// TestRevokeAdmin_RevokesAndAudits pins the path the guard above must not
// break: when the grant is still active the handler revokes it, records
// the revocation, and reports success. Without this the not-found
// assertion could pass on a handler that never revokes anything.
func TestRevokeAdmin_RevokesAndAudits(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID, targetID, targetPub := revokeFixture(t, db, q)

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}

	out, err := RevokeAdmin(deps)(authn.WithActor(ctx, actorID), &RevokeAdminInput{UserID: targetPub.String()})
	require.NoError(t, err)
	assert.True(t, out.Body.Ok)
	assert.Equal(t, []string{"admin.instance_admin.revoke"}, sink.actions(),
		"a revocation that landed must be audited exactly once")

	_, err = q.AdminFindInstanceAdminByUserId(ctx, targetID)
	assert.ErrorIs(t, err, sql.ErrNoRows, "the grant must no longer be active after a successful revoke")
}

// grantAdmin gives uid an active instance admin grant.
func grantAdmin(t *testing.T, q *generated.Queries, uid uint32) {
	t.Helper()
	_, err := q.AdminGrantInstanceAdmin(context.Background(), generated.AdminGrantInstanceAdminParams{
		PublicID:        types.New(),
		UserID:          uid,
		GrantedByUserID: sql.NullInt32{},
	})
	require.NoError(t, err)
}

// userPublicID reads the public id of uid.
func userPublicID(t *testing.T, db *sql.DB, uid uint32) types.PublicID {
	t.Helper()
	var pub types.PublicID
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT public_id FROM users WHERE id = ?", uid).Scan(&pub))
	return pub
}

// enabledAdmins counts active grants whose user is enabled.
func enabledAdmins(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM instance_admins ia
		INNER JOIN users u ON u.id = ia.user_id AND u.enabled = TRUE
		WHERE ia.enabled = TRUE AND ia.revoked_at IS NULL`).Scan(&n))
	return n
}

// TestRevokeAdmin_SuspendedAdminsDoNotCount holds the last-admin guard to
// admins who can sign in: a grant held by a suspended user is no admin at
// all, so revoking the only enabled one is refused even though two grants
// are active.
func TestRevokeAdmin_SuspendedAdminsDoNotCount(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)
	actorID := setupNewUser(t, q)
	target := setupNewUser(t, q)
	suspended := setupNewUser(t, q)
	grantAdmin(t, q, target)
	grantAdmin(t, q, suspended)
	_, err = db.ExecContext(ctx, "UPDATE users SET enabled = FALSE WHERE id = ?", suspended)
	require.NoError(t, err)

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	_, err = RevokeAdmin(deps)(authn.WithActor(ctx, actorID), &RevokeAdminInput{UserID: userPublicID(t, db, target).String()})

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, "INSTANCE.ADMIN.LAST_ADMIN", prob.Type)
	assert.Empty(t, sink.actions())
	assert.Equal(t, 1, enabledAdmins(t, db), "the only enabled admin must keep the grant")
}

// TestRevokeAdmin_CrossRevokeKeepsOneAdmin races the only two admins
// revoking each other. Whatever the interleaving, the instance must end
// with one enabled admin and exactly one of the two calls must succeed.
func TestRevokeAdmin_CrossRevokeKeepsOneAdmin(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	for round := 0; round < 10; round++ {
		a, b, bPub := revokeFixture(t, db, q)
		aPub := userPublicID(t, db, a)
		deps := Deps{DB: db, Queries: q, Audit: &recordingSink{}}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, call := range []struct {
			actor  uint32
			target types.PublicID
		}{{a, bPub}, {b, aPub}} {
			wg.Add(1)
			go func(i int, actor uint32, target types.PublicID) {
				defer wg.Done()
				<-start
				_, errs[i] = RevokeAdmin(deps)(authn.WithActor(ctx, actor), &RevokeAdminInput{UserID: target.String()})
			}(i, call.actor, call.target)
		}
		close(start)
		wg.Wait()

		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			var prob *handlerutil.ProblemDetails
			require.ErrorAs(t, err, &prob)
			assert.Equal(t, "INSTANCE.ADMIN.LAST_ADMIN", prob.Type, "round %d", round)
		}
		require.Equal(t, 1, succeeded, "round %d: exactly one cross-revoke may win", round)
		require.Equal(t, 1, enabledAdmins(t, db), "round %d: one enabled admin must remain", round)
	}
}

// TestPatchUser_RefusesSelfSuspend holds the suspend path to the rule the
// revoke and delete paths already keep: an admin cannot act on their own
// account through it.
func TestPatchUser_RefusesSelfSuspend(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID, _, _ := revokeFixture(t, db, q)
	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	disabled := false
	in := &PatchUserInput{UserID: userPublicID(t, db, actorID).String()}
	in.Body.Enabled = &disabled
	_, err := PatchUser(deps)(authn.WithActor(ctx, actorID), in)

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, "INSTANCE.USER.SELF_SUSPEND", prob.Type)
	assert.Empty(t, sink.actions())
}

// TestPatchUser_RefusesSuspendingTheLastEnabledAdmin keeps the instance
// from losing its last usable admin to a suspension, which a revoke of the
// same admin would have been refused for.
func TestPatchUser_RefusesSuspendingTheLastEnabledAdmin(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)
	actorID := setupNewUser(t, q)
	onlyAdmin := setupNewUser(t, q)
	grantAdmin(t, q, onlyAdmin)

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	disabled := false
	in := &PatchUserInput{UserID: userPublicID(t, db, onlyAdmin).String()}
	in.Body.Enabled = &disabled
	_, err = PatchUser(deps)(authn.WithActor(ctx, actorID), in)

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, "INSTANCE.ADMIN.LAST_ADMIN", prob.Type)
	assert.Empty(t, sink.actions())
	assert.Equal(t, 1, enabledAdmins(t, db), "the admin must still be enabled")
}

// TestDeleteUser_RefusesDeletingTheLastEnabledAdmin holds the hard delete
// to the rule suspending and revoking follow: the instance keeps an
// enabled admin, the refusal records nothing, and the user survives.
func TestDeleteUser_RefusesDeletingTheLastEnabledAdmin(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "DELETE FROM instance_admins")
	require.NoError(t, err)
	actorID := setupNewUser(t, q)
	onlyAdmin := setupNewUser(t, q)
	grantAdmin(t, q, onlyAdmin)

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	confirm := true
	in := &DeleteUserInput{UserID: userPublicID(t, db, onlyAdmin).String()}
	in.Body.Confirm = &confirm
	_, err = DeleteUser(deps)(authn.WithActor(ctx, actorID), in)

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, "INSTANCE.ADMIN.LAST_ADMIN", prob.Type)
	assert.Empty(t, sink.actions())
	assert.Equal(t, 1, enabledAdmins(t, db), "the admin must survive the refused delete")
}

// TestDeleteUser_RefusesDeletingAWorkspacesLastOwner holds the hard delete
// to the rule member removal follows: a workspace keeps an owner, so the
// account of its only owner is not deleted out from under it.
func TestDeleteUser_RefusesDeletingAWorkspacesLastOwner(t *testing.T) {
	db := requireSetupDB(t)
	q := generated.New(db)
	ctx := context.Background()

	actorID := setupNewUser(t, q)
	grantAdmin(t, q, actorID)
	owner := setupNewUser(t, q)
	wsPub := types.New()
	res, err := db.ExecContext(ctx, `INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub, "last-owner-"+wsPub.String()[:8], "Last owner")
	require.NoError(t, err)
	wsID, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		types.New(), wsID, owner)
	require.NoError(t, err)

	sink := &recordingSink{}
	deps := Deps{DB: db, Queries: q, Audit: sink}
	confirm := true
	in := &DeleteUserInput{UserID: userPublicID(t, db, owner).String()}
	in.Body.Confirm = &confirm
	_, err = DeleteUser(deps)(authn.WithActor(ctx, actorID), in)

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &prob)
	assert.Equal(t, "WS.MEMBER.LAST_OWNER", prob.Type)
	assert.Empty(t, sink.actions())
	var n int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = ?", owner).Scan(&n))
	assert.Equal(t, 1, n, "the owner's account must survive the refused delete")
}

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

// revokeRaceDB is a generated.DBTX decorator that opens the check-then-act
// window the revoke handler races against: the first time the revoke
// UPDATE is executed, a competing revocation runs to completion first, so
// the handler's own statement matches no row. Embedding *sql.DB leaves
// every other DBTX method untouched.
type revokeRaceDB struct {
	*sql.DB
	once    sync.Once
	compete func(ctx context.Context)
}

// ExecContext fires the competing revocation once, immediately before the
// handler's revoke statement reaches the server.
func (d *revokeRaceDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if strings.Contains(query, "UPDATE instance_admins") && strings.Contains(query, "revoked_at = NOW()") {
		d.once.Do(func() { d.compete(ctx) })
	}
	return d.DB.ExecContext(ctx, query, args...)
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

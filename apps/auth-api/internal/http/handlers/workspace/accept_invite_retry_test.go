package workspace

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// inviteUseCount reads the invite's redemption counter straight from
// the row so the assertion does not depend on the response body.
func inviteUseCount(t *testing.T, q *generated.Queries, token string) int64 {
	t.Helper()
	invite, err := q.FindWorkspaceInviteByTokenHash(context.Background(), auth.HashOpaque(token))
	require.NoError(t, err)
	return int64(invite.UseCount)
}

// TestAcceptInviteSurvivesTransientLockContention is the accept-side
// regression for the deadlock that reached users as a 500.
//
// Joining a workspace writes the membership, a personal calendar layer,
// a holiday subscription and a `workspace.member.added` event row in one
// transaction. A deadlock on any of them rolls the whole transaction
// back, so retrying the failed statement would re-issue it against a
// transaction the server has already discarded — the retry has to
// restart the transaction.
//
// Contention here is a lock on the invitee's users row, which the
// membership insert needs for its foreign-key check; it is released
// after the first attempt has timed out so a retrying handler has
// something to succeed at. The use_count assertion is the other half:
// the attempt that rolled back must not leave its conditional increment
// behind, or a retried join would silently consume two slots of the
// invite's cap.
func TestAcceptInviteSurvivesTransientLockContention(t *testing.T) {
	inst := requireWorkspaceTxDB(t)
	ctx := context.Background()

	shared := generated.New(inst.DB)
	workspaceID := raceWorkspace(t, shared)
	token := raceInvite(t, shared, workspaceID, 5)
	uid := raceNewUser(t, shared)

	db := impatientPool(t, inst.DSN)
	deps := InviteDeps{Deps: Deps{DB: db, Queries: generated.New(db), Audit: audit.NoopSink{}}}

	lockTx, err := inst.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback() }()
	var locked uint32
	require.NoError(t, lockTx.QueryRowContext(ctx,
		"SELECT id FROM users WHERE id = ? FOR UPDATE", uid).Scan(&locked))

	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		_ = lockTx.Rollback()
		close(released)
	}()

	out, err := AcceptInvite(deps)(authn.WithActor(ctx, uid), &AcceptInviteInput{Token: token})
	<-released
	require.NoError(t, err,
		"a lock the contending session released must not surface as a permanent failure")
	require.NotEmpty(t, out.Body.WorkspaceID)

	var members int
	require.NoError(t, inst.DB.QueryRow(
		"SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND user_id = ?",
		workspaceID, uid,
	).Scan(&members))
	assert.Equal(t, 1, members, "the retried attempt must leave exactly one membership")

	assert.Equal(t, int64(1), inviteUseCount(t, shared, token),
		"the rolled-back attempt must not leave its use_count increment behind")
}

// TestConcurrentAcceptsOnOneWorkspaceNeverFailInternally pins the shape
// the failure was first seen in: several people accepting invitations
// to the same workspace at once. Every accept inserts into the same
// workspace's member and calendar tables and appends an event row, so
// they contend on shared parents and InnoDB rolls one of them back.
// With max_uses covering every racer, none of them has a legitimate
// reason to fail — any error here is the transient one leaking out.
func TestConcurrentAcceptsOnOneWorkspaceNeverFailInternally(t *testing.T) {
	inst := requireWorkspaceTxDB(t)
	ctx := context.Background()

	q := generated.New(inst.DB)
	workspaceID := raceWorkspace(t, q)

	const racers = 8
	deps := InviteDeps{Deps: Deps{DB: inst.DB, Queries: q, Audit: audit.NoopSink{}}}
	handler := AcceptInvite(deps)

	// One invite per racer, each with a slot of its own, so nobody is
	// expected to lose: EXHAUSTED would confuse the signal.
	tokens := make([]string, racers)
	uids := make([]uint32, racers)
	for i := range tokens {
		tokens[i] = raceInvite(t, q, workspaceID, 1)
		uids[i] = raceNewUser(t, q)
	}

	// Seed one member serially first. A workspace reached through the
	// API always has an owner, and that first join is what materialises
	// the workspace-wide holiday calendar; racing the very first join is
	// a different problem (see the lazy-create note in the report) and
	// would mask the contention this test is about.
	seedToken := raceInvite(t, q, workspaceID, 1)
	seedUID := raceNewUser(t, q)
	_, err := handler(authn.WithActor(ctx, seedUID), &AcceptInviteInput{Token: seedToken})
	require.NoError(t, err, "seed join must succeed")

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errs   []error
		start  = make(chan struct{})
		joined int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			<-start
			_, err := handler(authn.WithActor(ctx, uids[slot]), &AcceptInviteInput{Token: tokens[slot]})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			joined++
		}(i)
	}
	close(start)
	wg.Wait()

	require.Empty(t, errs, "concurrent joins must not surface a transient database failure")
	assert.Equal(t, racers, joined)

	var members int
	require.NoError(t, inst.DB.QueryRow(
		"SELECT COUNT(*) FROM workspace_members WHERE workspace_id = ? AND enabled = TRUE",
		workspaceID,
	).Scan(&members))
	assert.Equal(t, racers+1, members, "every racer must end up a member exactly once, alongside the seed")
}

package mutationlog_test

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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "mutationlog_test"})

// tenant is one workspace and one member, which is everything either
// log row needs to be insertable.
type tenant struct {
	workspaceID       uint32
	workspacePublicID dbtype.PublicID
	userID            uint32
}

func seed(ctx context.Context, t *testing.T, db *sql.DB) tenant {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// workspaces.slug is globally unique and cut to ten characters, so
	// it is taken off the low-order end: the leading digits of a
	// nanosecond timestamp only change once a second.
	slugPart := suffix[len(suffix)-10:]

	run := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	workspacePublicID := dbtype.New()
	wsID := run(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		workspacePublicID, "ws-"+slugPart, "MutationLog "+suffix)
	userID := run(`INSERT INTO users (public_id, email, display_name, locale, timezone)
		VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "mutationlog+"+suffix+"@example.test", "MutationLog Tester")
	run(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		dbtype.New(), wsID, userID)

	return tenant{workspaceID: wsID, workspacePublicID: workspacePublicID, userID: userID}
}

// counts is the pair of numbers every assertion in this file is about.
// Taken as deltas inside the tenant's own workspace so a parallel run
// against the shared database cannot change the answer.
type counts struct {
	events int
	audits int
}

func trail(ctx context.Context, t *testing.T, db *sql.DB, tn tenant, kind eventbus.Kind, action string) counts {
	t.Helper()
	var c counts
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`,
		tn.workspaceID, string(kind)).Scan(&c.events))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		tn.workspaceID, action).Scan(&c.audits))
	return c
}

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := shared.Start(context.Background())
	require.NoError(t, err)
	return inst.DB
}

const (
	trailKind   = eventbus.IntakeItemCreated
	trailAction = "intake.create"
)

// TestRecordLeavesBothTraces is the base claim: one call, one row in
// each log. It fails if either write is dropped, which is what makes it
// the check that the pair cannot quietly become a single.
func TestRecordLeavesBothTraces(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))

	before := trail(ctx, t, db, tn, trailKind, trailAction)
	rec.Record(ctx, mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
		EventType:    trailKind,
		AuditAction:  trailAction,
		ResourceType: "intake_item",
		ResourceID:   dbtype.New().String(),
		Payload:      map[string]any{"title": "Bring the projector"},
		CallSite:     "mutationlog_test.Record",
	})
	after := trail(ctx, t, db, tn, trailKind, trailAction)

	require.Equal(t, before.events+1, after.events,
		"a recorded change must reach the event log, or it appears on no timeline and fires no webhook")
	require.Equal(t, before.audits+1, after.audits,
		"a recorded change must reach the audit log, or an administrator querying the action finds nothing")
}

// TestRecordDescribesTheChangeTheSameWayInBothLogs proves the payload
// is one description rather than two that can drift. A reader comparing
// the tables must not have to work out which is stale.
func TestRecordDescribesTheChangeTheSameWayInBothLogs(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))

	resource := dbtype.New().String()
	rec.Record(ctx, mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
		EventType:    trailKind,
		AuditAction:  trailAction,
		ResourceType: "intake_item",
		ResourceID:   resource,
		Payload:      map[string]any{"intakeItemId": resource, "title": "Bring the projector"},
		CallSite:     "mutationlog_test.Record",
	})

	var eventPayload, auditMetadata string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, string(trailKind)).Scan(&eventPayload))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT metadata_json FROM audit_logs WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, trailAction).Scan(&auditMetadata))
	require.JSONEq(t, eventPayload, auditMetadata,
		"both rows describe one change, so they carry one description")

	var actorID sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT actor_user_id FROM audit_logs WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`,
		tn.workspaceID, trailAction).Scan(&actorID))
	require.True(t, actorID.Valid, "the audit row must name who made the change")
	require.Equal(t, int32(tn.userID), actorID.Int32) //#nosec G115 -- seeded id, fits int32
}

func TestRecordAttributesAgentWithoutHumanActor(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	agent := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent"})
	rec := mutationlog.New(db, generated.New(db))
	publicAgentID := agent.AgentPublicID

	rec.Record(ctx, mutationlog.Actor{AgentID: agent.AgentID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
		EventType:    trailKind,
		AuditAction:  trailAction,
		ResourceType: "intake_item",
		ResourceID:   dbtype.New().String(),
		Payload:      map[string]any{"authorKind": "agent", "authorAgentId": publicAgentID},
		CallSite:     "mutationlog_test.RecordAgent",
	})

	var eventUser, eventAgent sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_user_id, actor_agent_id FROM events
WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, tn.workspaceID, string(trailKind)).
		Scan(&eventUser, &eventAgent))
	require.False(t, eventUser.Valid, "agent event attribution must not invent a human actor")
	require.True(t, eventAgent.Valid)
	require.Equal(t, int64(agent.AgentID), int64(eventAgent.Int32))

	var auditUser sql.NullInt32
	var metadata string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_user_id, metadata_json FROM audit_logs
WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`, tn.workspaceID, trailAction).
		Scan(&auditUser, &metadata))
	require.False(t, auditUser.Valid, "agent audit attribution must not use a delegator as author")
	require.Contains(t, metadata, publicAgentID, "audit metadata carries the public agent identity")
}

func TestRecordRejectsConflictingAgentActorsAndDeduplicatesMatch(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	agentA := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent A"})
	agentB := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent B"})
	rec := mutationlog.New(db, generated.New(db))
	mutation := func() mutationlog.Mutation {
		return mutationlog.Mutation{
			EventType:    trailKind,
			AuditAction:  trailAction,
			ResourceType: "intake_item",
			ResourceID:   dbtype.New().String(),
			Payload:      map[string]any{"title": "agent change"},
			CallSite:     "mutationlog_test.RecordAgentConflict",
		}
	}
	before := trail(ctx, t, db, tn, trailKind, trailAction)

	conflictingHumanAndAgent := mutation()
	rec.Record(ctx, mutationlog.Actor{UserID: tn.userID, AgentID: agentA.AgentID, WorkspaceID: tn.workspaceID}, conflictingHumanAndAgent)
	require.Equal(t, before, trail(ctx, t, db, tn, trailKind, trailAction),
		"one actor cannot be both a human and an agent")

	differentLegacyAgent := mutation()
	legacyAgentID := int64(agentB.AgentID)
	differentLegacyAgent.ActorAgentID = &legacyAgentID
	rec.Record(ctx, mutationlog.Actor{AgentID: agentA.AgentID, WorkspaceID: tn.workspaceID}, differentLegacyAgent)
	require.Equal(t, before, trail(ctx, t, db, tn, trailKind, trailAction),
		"new and legacy agent attribution must agree")

	sameLegacyAgent := mutation()
	sameAgentID := int64(agentA.AgentID)
	sameLegacyAgent.ActorAgentID = &sameAgentID
	rec.Record(ctx, mutationlog.Actor{AgentID: agentA.AgentID, WorkspaceID: tn.workspaceID}, sameLegacyAgent)
	after := trail(ctx, t, db, tn, trailKind, trailAction)
	require.Equal(t, before.events+1, after.events, "matching legacy attribution is deduplicated")
	require.Equal(t, before.audits+1, after.audits)
	var eventAgent sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_agent_id FROM events
WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, tn.workspaceID, string(trailKind)).Scan(&eventAgent))
	require.True(t, eventAgent.Valid)
	require.Equal(t, int64(agentA.AgentID), int64(eventAgent.Int32))
}

func TestRecordInTxActorConflictReturnsErrorAndRollsBack(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	agent := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent"})
	rec := mutationlog.New(db, generated.New(db))
	before := trail(ctx, t, db, tn, trailKind, trailAction)
	hookCalled := false
	var recordErr error

	txErr := dbretry.InTx(ctx, db, "mutationlog_test.actor_conflict_rollback", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = 'partial mutation' WHERE id = ?`, tn.userID); err != nil {
			return err
		}
		tx.AfterCommit(func() { hookCalled = true })
		recordErr = rec.RecordInTx(ctx, tx,
			mutationlog.Actor{UserID: tn.userID, AgentID: agent.AgentID, WorkspaceID: tn.workspaceID},
			mutationlog.Mutation{
				EventType: trailKind, AuditAction: trailAction, ResourceType: "intake_item",
				ResourceID: dbtype.New().String(), CallSite: "mutationlog_test.ActorConflictRollback",
			},
		)
		return recordErr
	})

	assert.Error(t, recordErr, "RecordInTx must report ambiguous human and agent attribution")
	assert.ErrorIs(t, recordErr, mutationlog.ErrActorConflict)
	assert.Error(t, txErr, "the enclosing transaction must fail with the recording error")
	assert.ErrorIs(t, txErr, mutationlog.ErrActorConflict)
	assert.Equal(t, before, trail(ctx, t, db, tn, trailKind, trailAction), "neither event nor audit row may be written")
	var displayName string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = ?`, tn.userID).Scan(&displayName))
	assert.Equal(t, "MutationLog Tester", displayName, "the preceding domain update must roll back")
	assert.False(t, hookCalled, "after-commit hooks must not fire for the rolled-back transaction")
}

func TestRecordInTxActorConflictCannotBeSwallowed(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	agentA := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent A"})
	agentB := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent B"})
	rec := mutationlog.New(db, generated.New(db))
	before := trail(ctx, t, db, tn, trailKind, trailAction)
	legacyAgentID := int64(agentB.AgentID)
	var recordErr error
	hookCalled := false

	txErr := dbretry.InTx(ctx, db, "mutationlog_test.actor_conflict_swallowed", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = 'partial mutation' WHERE id = ?`, tn.userID); err != nil {
			return err
		}
		tx.AfterCommit(func() { hookCalled = true })
		recordErr = rec.RecordInTx(ctx, tx,
			mutationlog.Actor{AgentID: agentA.AgentID, WorkspaceID: tn.workspaceID},
			mutationlog.Mutation{
				EventType: trailKind, AuditAction: trailAction, ResourceType: "intake_item",
				ResourceID: dbtype.New().String(), ActorAgentID: &legacyAgentID,
				CallSite: "mutationlog_test.ActorConflictSwallowed",
			},
		)
		return nil // Deliberately ignore RecordInTx's error; tx.Fail must forbid commit.
	})

	assert.Error(t, recordErr, "the new and legacy agent identities conflict")
	assert.ErrorIs(t, recordErr, mutationlog.ErrActorConflict)
	assert.Error(t, txErr, "ignoring the recording error must not allow commit")
	assert.ErrorIs(t, txErr, mutationlog.ErrActorConflict)
	assert.Equal(t, before, trail(ctx, t, db, tn, trailKind, trailAction), "neither event nor audit row may be written")
	var displayName string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = ?`, tn.userID).Scan(&displayName))
	assert.Equal(t, "MutationLog Tester", displayName, "the preceding domain update must roll back")
	assert.False(t, hookCalled, "after-commit hooks must not fire for the rolled-back transaction")
}

func TestRecordStrictReturnsErrorForConflictingAgentAttribution(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	agentA := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent A"})
	agentB := helpers.SeedAgent(t, db, tn.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Mutation Agent B"})
	rec := mutationlog.New(db, generated.New(db))
	before := trail(ctx, t, db, tn, trailKind, trailAction)
	legacyAgentID := int64(agentB.AgentID)

	err := rec.RecordStrict(ctx,
		mutationlog.Actor{AgentID: agentA.AgentID, WorkspaceID: tn.workspaceID},
		mutationlog.Mutation{
			EventType: trailKind, AuditAction: trailAction, ResourceType: "intake_item",
			ResourceID: dbtype.New().String(), ActorAgentID: &legacyAgentID,
			CallSite: "mutationlog_test.StrictActorConflict",
		},
	)
	assert.Error(t, err, "RecordStrict must expose the identity conflict")
	assert.ErrorIs(t, err, mutationlog.ErrActorConflict)
	assert.Equal(t, before, trail(ctx, t, db, tn, trailKind, trailAction), "no event or audit row may be written")
}

// TestRecordInTxLeavesBothTracesOnCommit covers the transactional entry
// point's success path.
func TestRecordInTxLeavesBothTracesOnCommit(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))

	before := trail(ctx, t, db, tn, trailKind, trailAction)
	require.NoError(t, dbretry.InTx(ctx, db, "mutationlog_test.commit", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return rec.RecordInTx(ctx, tx, mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
			EventType:    trailKind,
			AuditAction:  trailAction,
			ResourceType: "intake_item",
			ResourceID:   dbtype.New().String(),
			CallSite:     "mutationlog_test.RecordInTx",
		})
	}))
	after := trail(ctx, t, db, tn, trailKind, trailAction)

	require.Equal(t, before.events+1, after.events)
	require.Equal(t, before.audits+1, after.audits,
		"the audit row waits for the commit, so it must arrive once the commit happens")
}

// TestRecordInTxLeavesNoTraceOnRollback is the reason the audit row is
// registered on the commit rather than written inline.
//
// Written inline it would go out on the pool handle, outside the
// transaction, and survive the rollback — leaving audit_logs asserting a
// change that never happened, which is the same class of defect as
// losing one that did. The event half is covered by the transaction
// itself; this asserts both so a later change cannot restore the inline
// write and still pass.
func TestRecordInTxLeavesNoTraceOnRollback(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))

	abandoned := errors.New("the work this record describes did not complete")
	before := trail(ctx, t, db, tn, trailKind, trailAction)
	err := dbretry.InTx(ctx, db, "mutationlog_test.rollback", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if rerr := rec.RecordInTx(ctx, tx, mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
			EventType:    trailKind,
			AuditAction:  trailAction,
			ResourceType: "intake_item",
			ResourceID:   dbtype.New().String(),
			CallSite:     "mutationlog_test.RecordInTx",
		}); rerr != nil {
			return rerr
		}
		return abandoned
	})
	require.ErrorIs(t, err, abandoned)
	after := trail(ctx, t, db, tn, trailKind, trailAction)

	require.Equal(t, before.events, after.events,
		"the event joined the transaction, so a rollback must take it")
	require.Equal(t, before.audits, after.audits,
		"an audit row for a rolled-back change answers an administrator's query with something that never happened")
}

// TestRecordTxAuditWritesOnlyTheAuditRow covers the entry point for a
// change whose event a shared transactional helper already appended.
// Appending it again here would put the change on the timeline twice.
func TestRecordTxAuditWritesOnlyTheAuditRow(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))

	before := trail(ctx, t, db, tn, trailKind, trailAction)
	rec.RecordTxAudit(ctx, mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}, mutationlog.Mutation{
		AuditAction:  trailAction,
		ResourceType: "intake_item",
		ResourceID:   dbtype.New().String(),
		CallSite:     "mutationlog_test.RecordTxAudit (a shared helper appended the event)",
	})
	after := trail(ctx, t, db, tn, trailKind, trailAction)

	require.Equal(t, before.events, after.events)
	require.Equal(t, before.audits+1, after.audits)
}

// TestIncompleteMutationRecordsNeitherHalf covers the value assembled at
// runtime, which the static guard cannot read. Half a record is worse
// than none, because the table that got a row reads as a complete
// answer to whoever queries it.
func TestIncompleteMutationRecordsNeitherHalf(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	tn := seed(ctx, t, db)
	rec := mutationlog.New(db, generated.New(db))
	act := mutationlog.Actor{UserID: tn.userID, WorkspaceID: tn.workspaceID}

	for _, tc := range []struct {
		name string
		m    mutationlog.Mutation
	}{
		{"no audit action", mutationlog.Mutation{EventType: trailKind, ResourceType: "intake_item"}},
		{"no event kind", mutationlog.Mutation{AuditAction: trailAction, ResourceType: "intake_item"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := trail(ctx, t, db, tn, trailKind, trailAction)
			rec.Record(ctx, act, tc.m)
			after := trail(ctx, t, db, tn, trailKind, trailAction)
			require.Equal(t, before, after)
		})
	}

	t.Run("no workspace", func(t *testing.T) {
		before := trail(ctx, t, db, tn, trailKind, trailAction)
		rec.Record(ctx, mutationlog.Actor{UserID: tn.userID}, mutationlog.Mutation{
			EventType:   trailKind,
			AuditAction: trailAction,
		})
		after := trail(ctx, t, db, tn, trailKind, trailAction)
		require.Equal(t, before, after, "a row belonging to no tenant is not a record of anything")
	})
}

// TestNilRecorderIsSafe proves an unwired recorder cannot take a
// request down. It says so in the log instead; a panic here would turn
// a missing dependency into an outage on the first mutation.
func TestNilRecorderIsSafe(t *testing.T) {
	t.Parallel()
	var rec *mutationlog.Recorder
	require.NotPanics(t, func() {
		rec.Record(context.Background(), mutationlog.Actor{UserID: 1, WorkspaceID: 1}, mutationlog.Mutation{
			EventType:   trailKind,
			AuditAction: trailAction,
		})
	})
}

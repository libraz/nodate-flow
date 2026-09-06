package signals

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	goog "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/google"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var sharedDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "signals_handler_test"})

// recordedAction is the audit action every ingestion writes, whatever
// transport it arrived over: one operation, one action name.
const recordedAction = "signal.create"

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := sharedDB.Start(context.Background())
	require.NoError(t, err)
	return inst.DB
}

// trail is the pair of counts every assertion here is about, scoped to
// the test's own workspace so a parallel run cannot change the answer.
type trail struct {
	events int
	audits int
}

func readTrail(ctx context.Context, t *testing.T, db *sql.DB, wsID uint32) trail {
	t.Helper()
	var c trail
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`,
		wsID, string(eventbus.SignalAttached)).Scan(&c.events))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		wsID, recordedAction).Scan(&c.audits))
	return c
}

// seedWorkspace creates the tenant a delivery is routed to.
func seedWorkspace(ctx context.Context, t *testing.T, db *sql.DB) uint32 {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// workspaces.slug is globally unique and cut to ten characters, so it
	// is taken off the low-order end: the leading digits of a nanosecond
	// timestamp only change once a second.
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		types.New(), "ws-"+suffix[len(suffix)-10:], "Signals "+suffix)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// mapSender claims an external sender for the workspace, which is the
// only routing input an authenticated delivery carries.
func mapSender(ctx context.Context, t *testing.T, db *sql.DB, wsID uint32, provider, externalKey string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO integration_source_mappings (public_id, workspace_id, provider, external_key, label)
		 VALUES (?, ?, ?, ?, ?)`,
		types.New(), wsID, provider, externalKey, "test "+provider+" source")
	require.NoError(t, err)
}

// deliver drives a chi-level webhook handler and returns the status and
// the decoded body.
func deliver(t *testing.T, h http.HandlerFunc, req *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, req)
	body := map[string]any{}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
			"webhook response is not JSON: %s", rec.Body.String())
	}
	return rec.Code, body
}

// TestSlackDeliveryRecordsBothLogsAndARedeliveryDoesNot pairs the two
// cases that have to hold together.
//
// A first delivery persists a signals row, so it appears on the timeline
// and answers an audit query by action name. A Slack retry of the same
// event_id collides on the dedupe key and writes no row, so it must add
// nothing to either log — a test that only showed the first delivery
// recording would leave a redelivery free to log an ingestion that never
// happened.
func TestSlackDeliveryRecordsBothLogsAndARedeliveryDoesNot(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	wsID := seedWorkspace(ctx, t, db)

	const secret = "slack-signing-secret-for-record-test"
	teamID := fmt.Sprintf("T%d", time.Now().UnixNano())
	mapSender(ctx, t, db, wsID, "slack", teamID)

	q := generated.New(db)
	handler := HandleSlackWebhook(Deps{
		DB:                 db,
		Queries:            q,
		Mutations:          mutationlog.New(db, q),
		SlackSigningSecret: secret,
	})

	body, err := json.Marshal(map[string]any{
		"type":     "event_callback",
		"event_id": "Ev" + teamID,
		"team_id":  teamID,
		"event":    map[string]any{"type": "app_mention", "text": "hello"},
	})
	require.NoError(t, err)
	signed := func() *http.Request {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
		req.Header.Set(sl.TimestampHeader, ts)
		req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, secret))
		return req
	}

	before := readTrail(ctx, t, db, wsID)
	status, first := deliver(t, handler, signed())
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first["id"])

	afterFirst := readTrail(ctx, t, db, wsID)
	require.Equal(t, before.events+1, afterFirst.events,
		"an ingested delivery must reach the event log, or it appears on no timeline")
	require.Equal(t, before.audits+1, afterFirst.audits,
		"an ingested delivery must reach the audit log, or an administrator querying the action finds nothing")

	status, second := deliver(t, handler, signed())
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, true, second["duplicate"], "the retry must be reported as a duplicate")

	afterRetry := readTrail(ctx, t, db, wsID)
	require.Equal(t, afterFirst, afterRetry,
		"a retry that wrote no signals row must record no ingestion in either log")
}

// TestGoogleDeliveryRecordsBothLogsAndARedeliveryDoesNot is the same
// pair for the Drive push receiver, where a retry repeats the channel id
// and the message number and therefore collides on the dedupe key.
func TestGoogleDeliveryRecordsBothLogsAndARedeliveryDoesNot(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	wsID := seedWorkspace(ctx, t, db)

	const channelToken = "google-channel-token-for-record-test"
	channelID := fmt.Sprintf("chan-%d", time.Now().UnixNano())
	mapSender(ctx, t, db, wsID, "google", channelID)

	q := generated.New(db)
	handler := HandleGoogleWebhook(Deps{
		DB:                 db,
		Queries:            q,
		Mutations:          mutationlog.New(db, q),
		GoogleChannelToken: channelToken,
	})

	push := func(messageNumber string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/google",
			bytes.NewReader([]byte(`{"kind":"drive#change"}`)))
		req.Header.Set(goog.HeaderChannelToken, channelToken)
		req.Header.Set(goog.HeaderChannelID, channelID)
		req.Header.Set(headerGoogleMessageNumber, messageNumber)
		req.Header.Set(goog.HeaderResourceState, "update")
		return req
	}

	before := readTrail(ctx, t, db, wsID)
	status, first := deliver(t, handler, push("1"))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first["id"])

	afterFirst := readTrail(ctx, t, db, wsID)
	require.Equal(t, before.events+1, afterFirst.events,
		"an ingested notification must reach the event log, or it appears on no timeline")
	require.Equal(t, before.audits+1, afterFirst.audits,
		"an ingested notification must reach the audit log, or an administrator querying the action finds nothing")

	status, retry := deliver(t, handler, push("1"))
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, true, retry["duplicate"], "the retry must be reported as a duplicate")

	afterRetry := readTrail(ctx, t, db, wsID)
	require.Equal(t, afterFirst, afterRetry,
		"a retry that wrote no signals row must record no ingestion in either log")

	// A different notification on the same channel is a distinct
	// ingestion, so the pair moves again.
	status, next := deliver(t, handler, push("2"))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEqual(t, first["id"], next["id"])

	afterSecond := readTrail(ctx, t, db, wsID)
	require.Equal(t, afterFirst.events+1, afterSecond.events)
	require.Equal(t, afterFirst.audits+1, afterSecond.audits)
}

// TestWebhookIngestionRecordsNoActor holds the actor column to what the
// transport can honestly say: a delivery arrives from outside with no
// authenticated user behind it, so both rows name nobody rather than
// attributing the ingestion to a fabricated id.
func TestWebhookIngestionRecordsNoActor(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	wsID := seedWorkspace(ctx, t, db)

	const channelToken = "google-channel-token-for-actor-test"
	channelID := fmt.Sprintf("actor-chan-%d", time.Now().UnixNano())
	mapSender(ctx, t, db, wsID, "google", channelID)

	q := generated.New(db)
	handler := HandleGoogleWebhook(Deps{
		DB:                 db,
		Queries:            q,
		Mutations:          mutationlog.New(db, q),
		GoogleChannelToken: channelToken,
	})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/google",
		bytes.NewReader([]byte(`{"kind":"drive#change"}`)))
	req.Header.Set(goog.HeaderChannelToken, channelToken)
	req.Header.Set(goog.HeaderChannelID, channelID)
	req.Header.Set(headerGoogleMessageNumber, "1")
	req.Header.Set(goog.HeaderResourceState, "sync")
	status, _ := deliver(t, handler, req)
	require.Equal(t, http.StatusAccepted, status)

	var eventActor, auditActor sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT actor_user_id FROM events WHERE workspace_id = ? AND type = ? ORDER BY id DESC LIMIT 1`,
		wsID, string(eventbus.SignalAttached)).Scan(&eventActor))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT actor_user_id FROM audit_logs WHERE workspace_id = ? AND action = ? ORDER BY id DESC LIMIT 1`,
		wsID, recordedAction).Scan(&auditActor))
	require.False(t, eventActor.Valid, "a delivery has no authenticated user, so the event row names none")
	require.False(t, auditActor.Valid, "a delivery has no authenticated user, so the audit row names none")
}

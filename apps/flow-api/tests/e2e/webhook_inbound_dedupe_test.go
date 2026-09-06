package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	gh "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/github"
	goog "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/google"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// headerGoogleMessageNumber is Google's per-delivery counter. It is spelled
// out here rather than imported so the test states the wire contract
// independently of the handler's own constant.
const headerGoogleMessageNumber = "X-Goog-Message-Number"

// countingEnqueuer records every judge dispatch so a test can assert a
// duplicate delivery does not wake the judge a second time.
type countingEnqueuer struct {
	mu  sync.Mutex
	ids []int64
}

func (c *countingEnqueuer) EnqueueForSignal(_ context.Context, signalID int64, _ uint32, _ signalkinds.Kind) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, signalID)
	return nil
}

func (c *countingEnqueuer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ids)
}

// webhookResponse is the JSON body the inbound webhook handlers return.
type webhookResponse struct {
	ID        string `json:"id"`
	Duplicate bool   `json:"duplicate"`
	Linked    bool   `json:"linked"`
}

// webhookDeps builds the dependency bundle the inbound webhook handlers
// run with: the pooled handle, one sqlc handle over it, and a mutation
// recorder built from that same pair, which is what the router hands the
// handlers and therefore the only shape worth driving here.
//
// Every webhook case in this package goes through it rather than
// composing signals.Deps inline. A struct literal that leaves a field out
// still compiles, and a handler holding a nil recorder drops the rows it
// was asked to write with a log line instead of an error — so the case
// would exercise a configuration production never has and nothing would
// go red. Callers set the per-provider secret on the returned value.
func webhookDeps() signals.Deps {
	q := generated.New(testDB)
	return signals.Deps{
		DB:        testDB,
		Queries:   q,
		Mutations: mutationlog.New(testDB, q),
	}
}

// githubDeliveryWithID builds a signed `issues.opened` delivery for one
// repository with the delivery id under the caller's control. Repeating
// that id is what makes a redelivery: GitHub echoes X-GitHub-Delivery
// verbatim when it retries, and that value is what the
// (workspace_id, source, external_id) dedupe key is built on.
func githubDeliveryWithID(t *testing.T, secret string, repoID int64, deliveryID, issueBody string) *http.Request {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"action": "opened",
		"repository": map[string]any{
			"id":        repoID,
			"full_name": fmt.Sprintf("acme/repo-%d", repoID),
		},
		"issue": map[string]any{
			"number": 1,
			"body":   issueBody,
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set(gh.EventHeader, "issues")
	req.Header.Set(gh.SignatureHeader, gh.Sign(payload, secret))
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	return req
}

// ingestionEvent is the `events` row an inbound delivery appends: the
// half the timeline, the live feeds and the judge loop read.
type ingestionEvent struct {
	Type        string
	ActorUserID sql.NullInt32
	TaskID      sql.NullInt32
	Payload     map[string]any
}

// signalEvents reads the events rows in the tenant's workspace whose
// payload names the given signal. Both the workspace and the signal id
// are in the filter because the parallel suite shares one MySQL instance,
// so a reading scoped only by event type answers for other tenants too.
func signalEvents(t *testing.T, workspacePublicID, signalPublicID string) []ingestionEvent {
	t.Helper()
	rows, err := testDB.Query(
		`SELECT type, actor_user_id, task_id, payload_json
		 FROM events
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.signalId')) = ?
		 ORDER BY id`,
		workspacePublicID, signalPublicID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []ingestionEvent
	for rows.Next() {
		var row ingestionEvent
		var payload []byte
		require.NoError(t, rows.Scan(&row.Type, &row.ActorUserID, &row.TaskID, &payload))
		require.NoError(t, json.Unmarshal(payload, &row.Payload))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// ingestionAudit is the `audit_logs` row an inbound delivery writes: the
// half an administrator queries by action name. It answers a different
// question from the event row, so neither reading stands in for the
// other and a case that wants the change recorded asserts both.
type ingestionAudit struct {
	Action       string
	ResourceType string
	ResourceID   string
	ActorUserID  sql.NullInt32
	Metadata     map[string]any
}

// signalAuditRows reads the audit rows in the tenant's workspace that
// name the given signal as their resource.
func signalAuditRows(t *testing.T, workspacePublicID, signalPublicID string) []ingestionAudit {
	t.Helper()
	rows, err := testDB.Query(
		`SELECT action, resource_type, BIN_TO_UUID(resource_public_id, 0), actor_user_id, metadata_json
		 FROM audit_logs
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND resource_public_id = UUID_TO_BIN(?, 0)
		 ORDER BY id`,
		workspacePublicID, signalPublicID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []ingestionAudit
	for rows.Next() {
		var row ingestionAudit
		var metadata []byte
		require.NoError(t, rows.Scan(&row.Action, &row.ResourceType, &row.ResourceID, &row.ActorUserID, &metadata))
		if len(metadata) > 0 {
			require.NoError(t, json.Unmarshal(metadata, &row.Metadata))
		}
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// countIngestionRecords returns how many rows of each half of the
// ingestion record the tenant holds, whichever signal they name:
// `signal.attached` in events and `signal.create` in audit_logs.
func countIngestionRecords(t *testing.T, workspacePublicID string) (events, audits int) {
	t.Helper()
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND type = 'signal.attached'`,
		workspacePublicID).Scan(&events))
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM audit_logs
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND action = 'signal.create'`,
		workspacePublicID).Scan(&audits))
	return events, audits
}

// callWebhook drives a chi-level webhook handler directly. The inbound
// webhook routes are unauthenticated and resolve their workspace from the
// sender identity on the delivery, so each test maps its own sender to
// its own tenant instead of perturbing the shared server's configuration.
func callWebhook(t *testing.T, h http.HandlerFunc, req *http.Request) (int, webhookResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, req)
	var out webhookResponse
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
			"webhook response is not JSON: %s", rec.Body.String())
	}
	return rec.Code, out
}

// countSignals returns how many signals rows the tenant holds for the
// given source and external_id. Every assertion in this file is scoped to
// the test's own tenant and its own external ids, so the parallel suite
// sharing one MySQL instance cannot perturb it.
func countSignals(t *testing.T, workspacePublicID, source, externalID string) int {
	t.Helper()
	var n int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND source = ?
		   AND external_id = ?`,
		workspacePublicID, source, externalID).Scan(&n)
	require.NoError(t, err)
	return n
}

// mapWebhookSource claims an external webhook sender for the tenant's
// workspace by writing the routing row directly, so a test that drives a
// chi handler in-process does not have to stand up an admin session
// first. TestWebhookDeliveriesRouteToTheMappedWorkspace covers the REST
// surface that operators actually use.
func mapWebhookSource(t *testing.T, workspacePublicID, provider, externalKey string) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO integration_source_mappings (public_id, workspace_id, provider, external_key, label)
		 VALUES (UUID_TO_BIN(UUID(), 0),
		         (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)),
		         ?, ?, ?)`,
		workspacePublicID, provider, externalKey, "test "+provider+" source")
	require.NoError(t, err)
}

// TestGithubWebhookDedupesByDeliveryID verifies that a GitHub redelivery
// (identical X-GitHub-Delivery) lands as one signals row, returns the
// existing row's public id, and does not wake the judge a second time.
//
// GitHub redelivers on any non-2xx and offers a manual redeliver button,
// so the delivery id has always been on external_id — but the collided
// INSERT was not detected, and the handler answered with a freshly minted
// public id that matched no persisted row while still dispatching a judge
// run for it.
func TestGithubWebhookDedupesByDeliveryID(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "github-webhook-secret-for-dedupe-test" //#nosec G101 -- synthetic test fixture, never a real secret
	enq := &countingEnqueuer{}
	deps := webhookDeps()
	deps.GhWebhookSecret = secret
	deps.JudgeEnqueuer = enq
	handler := signals.HandleGithubWebhook(deps)

	// GitHub routes on repository.id; a per-test random id keeps the
	// instance-wide UNIQUE (provider, external_key) claim collision-free
	// under the parallel suite.
	repoSuffix, err := strconv.ParseInt(randomHex(6), 16, 64)
	require.NoError(t, err)
	repoID := repoSuffix + 1
	mapWebhookSource(t, tt.WorkspacePublicID, "github", strconv.FormatInt(repoID, 10))

	body, err := json.Marshal(map[string]any{
		"action": "opened",
		"repository": map[string]any{
			"id":        repoID,
			"full_name": "acme/widgets",
		},
	})
	require.NoError(t, err)

	deliveryID := "gh-delivery-" + randomHex(8)
	deliver := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
		req.Header.Set(gh.SignatureHeader, gh.Sign(body, secret))
		req.Header.Set(gh.EventHeader, "issues")
		req.Header.Set("X-GitHub-Delivery", deliveryID)
		return req
	}

	status, first := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first.ID)
	require.False(t, first.Duplicate, "the first delivery is a genuine insert")
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID))
	require.Equal(t, 1, enq.count(), "the first delivery must reach the judge")

	// GitHub's redelivery of the same delivery id.
	status, second := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so GitHub stops retrying")
	require.True(t, second.Duplicate, "the redelivery must be reported as a duplicate")
	require.Equal(t, first.ID, second.ID,
		"the redelivery must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID),
		"a GitHub redelivery must not create a second signals row")
	require.Equal(t, 1, enq.count(), "a duplicate delivery must not enqueue a second judge run")
}

// TestSlackWebhookDedupesByEventID verifies that a Slack event redelivery
// (identical top-level `event_id`) lands as one signals row, returns the
// existing row's public id, and does not wake the judge a second time.
//
// Slack retries an event up to three times when the receiver is slow or
// errors, so without event_id on external_id the (workspace_id, source,
// external_id) unique key never engages and one message becomes several
// signals plus several judge runs.
func TestSlackWebhookDedupesByEventID(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "slack-signing-secret-for-dedupe-test"
	enq := &countingEnqueuer{}
	deps := webhookDeps()
	deps.SlackSigningSecret = secret
	deps.JudgeEnqueuer = enq
	handler := signals.HandleSlackWebhook(deps)

	teamID := "T" + strings.ToUpper(randomHex(4))
	mapWebhookSource(t, tt.WorkspacePublicID, "slack", teamID)

	eventID := "Ev" + randomHex(8)
	body, err := json.Marshal(map[string]any{
		"type":     "event_callback",
		"event_id": eventID,
		"team_id":  teamID,
		"event": map[string]any{
			"type": "app_mention",
			"text": "hello",
		},
	})
	require.NoError(t, err)

	sign := func() *http.Request {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
		req.Header.Set(sl.TimestampHeader, ts)
		req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, secret))
		return req
	}

	status, first := callWebhook(t, handler, sign())
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first.ID)
	require.False(t, first.Duplicate, "the first delivery is a genuine insert")
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "slack", eventID))
	require.Equal(t, 1, enq.count(), "the first delivery must reach the judge")

	events, audits := countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "the delivery that landed appends one event")
	require.Equal(t, 1, audits, "the delivery that landed writes one audit row")

	// Slack's retry of the same event.
	status, second := callWebhook(t, handler, sign())
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so Slack stops retrying")
	require.True(t, second.Duplicate, "the redelivery must be reported as a duplicate")
	require.Equal(t, first.ID, second.ID,
		"the redelivery must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "slack", eventID),
		"a Slack retry must not create a second signals row")
	require.Equal(t, 1, enq.count(), "a duplicate delivery must not enqueue a second judge run")

	// The retry changed nothing, so it belongs in neither log; the counts
	// above are what make this reading evidence of suppression rather
	// than of a handler that never records.
	events, audits = countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "a suppressed retry must not put a second ingestion on the timeline")
	require.Equal(t, 1, audits, "a suppressed retry must not claim a second ingestion in the audit log")
}

// TestGoogleWebhookRecordsEveryDeliveryOnAChannel is the core regression
// for the Drive push receiver: a watch channel's id is fixed for the
// channel's whole lifetime, so keying external_id on it alone let the
// first notification in and silently discarded every later one while
// still answering 202. Pairing the channel id with the per-delivery
// message number keeps each notification distinct and still dedupes
// Google's retries of one notification.
func TestGoogleWebhookRecordsEveryDeliveryOnAChannel(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const channelToken = "google-channel-token-for-dedupe-test"
	enq := &countingEnqueuer{}
	deps := webhookDeps()
	deps.GoogleChannelToken = channelToken
	deps.JudgeEnqueuer = enq
	handler := signals.HandleGoogleWebhook(deps)

	channelID := "chan-" + randomHex(8)
	mapWebhookSource(t, tt.WorkspacePublicID, "google", channelID)
	push := func(messageNumber, resourceState string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/google",
			bytes.NewReader([]byte(`{"kind":"drive#change"}`)))
		req.Header.Set(goog.HeaderChannelToken, channelToken)
		req.Header.Set(goog.HeaderChannelID, channelID)
		req.Header.Set(headerGoogleMessageNumber, messageNumber)
		req.Header.Set(goog.HeaderResourceState, resourceState)
		return req
	}

	status, sync := callWebhook(t, handler, push("1", "sync"))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, sync.ID)
	require.False(t, sync.Duplicate)

	// A second, different notification on the SAME channel. This is the
	// delivery the previous key dropped.
	status, change := callWebhook(t, handler, push("2", "update"))
	require.Equal(t, http.StatusAccepted, status)
	require.False(t, change.Duplicate,
		"a different message number on the same channel is a new notification, not a duplicate")
	require.NotEqual(t, sync.ID, change.ID,
		"two notifications on one channel must be two distinct signals")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":1"))
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":2"),
		"the second notification on the channel must be recorded, not swallowed")
	require.Equal(t, 2, enq.count(), "both notifications must reach the judge")

	events, audits := countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 2, events, "each notification that landed appends its own event")
	require.Equal(t, 2, audits, "each notification that landed writes its own audit row")

	// Google's retry of notification 2.
	status, retry := callWebhook(t, handler, push("2", "update"))
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so Google stops retrying")
	require.True(t, retry.Duplicate, "the retry must be reported as a duplicate")
	require.Equal(t, change.ID, retry.ID,
		"the retry must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":2"),
		"a Google retry must not create a second signals row")
	require.Equal(t, 2, enq.count(), "a duplicate delivery must not enqueue a third judge run")

	// Two notifications recorded and a retry that adds nothing: the pair
	// is what distinguishes suppression from a path that records nothing.
	events, audits = countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 2, events, "a suppressed retry must not put a third ingestion on the timeline")
	require.Equal(t, 2, audits, "a suppressed retry must not claim a third ingestion in the audit log")
}

// TestGithubDeliveryIsRecordedInBothLogs states what an ingested delivery
// leaves behind. A delivery from outside changes the workspace like any
// other write, so it appears in both places such a change has to appear:
// a `signal.attached` row in `events`, which the timeline and the judge
// loop read, and a `signal.create` row in `audit_logs`, which an
// administrator queries by action name.
//
// Both are asserted because they answer different questions. A delivery
// that reaches only one table reads as a complete answer to whoever
// queries that table and as silence to whoever queries the other, so a
// reading of one says nothing about the state of the other.
func TestGithubDeliveryIsRecordedInBothLogs(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "github-webhook-secret-for-record-test" //#nosec G101 -- synthetic test fixture, never a real secret
	deps := webhookDeps()
	deps.GhWebhookSecret = secret
	handler := signals.HandleGithubWebhook(deps)

	repoID := randomRepoID()
	mapWebhookSource(t, tt.WorkspacePublicID, "github", strconv.FormatInt(repoID, 10))

	deliveryID := "gh-delivery-" + randomHex(8)
	status, delivered := callWebhook(t, handler,
		githubDeliveryWithID(t, secret, repoID, deliveryID, "no marker"))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, delivered.ID)
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID))

	events := signalEvents(t, tt.WorkspacePublicID, delivered.ID)
	require.Len(t, events, 1, "an ingested delivery appends exactly one event")
	require.Equal(t, "signal.attached", events[0].Type,
		"the ingestion is what the event names; every transport spells it the same way")
	require.False(t, events[0].ActorUserID.Valid,
		"there is no authenticated user behind a webhook delivery, so the row carries no actor rather than a fabricated one")
	require.False(t, events[0].TaskID.Valid,
		"a delivery whose body names no task stays workspace-scoped")
	require.Equal(t, delivered.ID, events[0].Payload["signalId"],
		"the event must name the signal the response handed back")
	require.Equal(t, "github", events[0].Payload["source"])
	require.Equal(t, "issues.opened", events[0].Payload["kind"],
		"the normalized event kind is what the constraint engine and timeline filters read")

	audits := signalAuditRows(t, tt.WorkspacePublicID, delivered.ID)
	require.Len(t, audits, 1, "an ingested delivery writes exactly one audit row")
	require.Equal(t, "signal.create", audits[0].Action,
		"the action name is how an administrator finds an ingestion, whatever transport performed it")
	require.Equal(t, "signal", audits[0].ResourceType)
	require.Equal(t, delivered.ID, audits[0].ResourceID,
		"the audit row must point at the signal that was filed")
	require.False(t, audits[0].ActorUserID.Valid,
		"a webhook delivery has no acting user in either log")
	require.Equal(t, delivered.ID, audits[0].Metadata["signalId"],
		"both logs describe the change from the same payload, so a reader comparing them cannot find one stale")
}

// TestGithubDeliveryWithATaskMarkerRecordsTheTaskLink covers the linked
// shape of the same recording: a `tnk:<uuid>` marker naming a task in the
// workspace that owns the sending repository puts that task on the event
// row, which is what makes the ingestion visible on the task's timeline
// rather than only in the workspace feed.
func TestGithubDeliveryWithATaskMarkerRecordsTheTaskLink(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "github-webhook-secret-for-marker-record-test" //#nosec G101 -- synthetic test fixture, never a real secret
	deps := webhookDeps()
	deps.GhWebhookSecret = secret
	handler := signals.HandleGithubWebhook(deps)

	repoID := randomRepoID()
	mapWebhookSource(t, tt.WorkspacePublicID, "github", strconv.FormatInt(repoID, 10))
	taskID := createTaskForAgent(t, tt, "Marked by an inbound delivery")

	status, delivered := callWebhook(t, handler,
		githubDeliveryWithID(t, secret, repoID, "gh-delivery-"+randomHex(8), "closes tnk:"+taskID))
	require.Equal(t, http.StatusAccepted, status)
	require.True(t, delivered.Linked, "a marker naming a task in this workspace must resolve")

	events := signalEvents(t, tt.WorkspacePublicID, delivered.ID)
	require.Len(t, events, 1)
	require.True(t, events[0].TaskID.Valid, "a resolved marker must put the task on the event row")
	require.Equal(t, int32(internalID(t, "tasks", taskID)), events[0].TaskID.Int32, //#nosec G115 -- test fixture id, well below int32
		"the event must link the task the marker named")

	audits := signalAuditRows(t, tt.WorkspacePublicID, delivered.ID)
	require.Len(t, audits, 1, "the audit half is written for a linked ingestion too")
	require.Equal(t, "signal.create", audits[0].Action)
	require.Equal(t, delivered.ID, audits[0].ResourceID)
}

// TestGithubRedeliveryRecordsNothingFurther pairs the two outcomes that
// make each other evidence. The first delivery is the control: it proves
// the recording path runs at all, so an empty second reading means the
// redelivery was suppressed rather than that nothing here ever records.
//
// A redelivery carries the same X-GitHub-Delivery, collides on
// (workspace_id, source, external_id) and writes no signal, so it changed
// nothing — and a change that did not happen must appear in neither log.
// An event would put a second ingestion on the timeline and wake
// consumers for it; an audit row would tell an administrator a delivery
// was filed twice.
func TestGithubRedeliveryRecordsNothingFurther(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "github-webhook-secret-for-redelivery-record-test" //#nosec G101 -- synthetic test fixture, never a real secret
	deps := webhookDeps()
	deps.GhWebhookSecret = secret
	handler := signals.HandleGithubWebhook(deps)

	repoID := randomRepoID()
	mapWebhookSource(t, tt.WorkspacePublicID, "github", strconv.FormatInt(repoID, 10))

	deliveryID := "gh-delivery-" + randomHex(8)
	deliver := func() *http.Request {
		return githubDeliveryWithID(t, secret, repoID, deliveryID, "no marker")
	}

	status, first := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status)
	require.False(t, first.Duplicate, "the first delivery is a genuine insert")

	events, audits := countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "the delivery that landed appends one event")
	require.Equal(t, 1, audits, "the delivery that landed writes one audit row")

	status, second := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so GitHub stops retrying")
	require.True(t, second.Duplicate, "the redelivery must be reported as a duplicate")
	require.Equal(t, first.ID, second.ID,
		"the redelivery must answer with the existing row's public id")
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID),
		"a redelivery must not create a second signals row")

	events, audits = countIngestionRecords(t, tt.WorkspacePublicID)
	require.Equal(t, 1, events, "a suppressed redelivery must not put a second ingestion on the timeline")
	require.Equal(t, 1, audits, "a suppressed redelivery must not claim a second ingestion in the audit log")
}

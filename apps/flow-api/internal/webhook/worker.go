// Package webhook implements the background webhook delivery worker. It
// subscribes to eventbus events via [Worker.Hook], creates pending
// delivery rows for matching subscriptions, and periodically processes
// those deliveries with POST requests signed over timestamp and body
// (see [sign] for the scheme, [webhookPayload] for what the body says).
// Each batch is first claimed atomically (FOR UPDATE SKIP LOCKED +
// status flip to 'delivering') so multiple worker replicas partition the
// queue instead of each delivering every row. Failed deliveries are
// retried with exponential backoff up to six attempts.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// retryDelays defines the exponential backoff schedule. After exhausting
// all slots the delivery is marked dead.
var retryDelays = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
}

// pollInterval is the ticker period for the delivery loop.
const pollInterval = 5 * time.Second

// deliveryConcurrency is how many subscriptions are delivered to at
// once. It bounds sockets and memory while leaving enough slots that a
// handful of slow endpoints cannot stall the rest; the per-subscription
// grouping in deliverBatch is what actually provides the isolation.
const deliveryConcurrency = 8

// defaultStrandedAfter is how long a delivery may stay in 'delivering'
// before the reaper assumes the worker holding it is gone.
//
// It has to sit well above the client timeout below, because a row
// legitimately in flight must never be requeued underneath the worker
// delivering it: that would produce a duplicate POST for a request that
// was about to succeed. Ten seconds of timeout against five minutes of
// grace leaves a wide margin even when a machine is badly overloaded.
const defaultStrandedAfter = 5 * time.Minute

// batchSize is the maximum number of pending deliveries fetched per tick.
const batchSize = 50

// maxResponseBodyLen is the maximum number of bytes stored from the
// target's response body (for debugging failed deliveries).
const maxResponseBodyLen = 4096

// Worker is the background webhook delivery processor.
type Worker struct {
	db      *sql.DB
	queries *generated.Queries
	client  *http.Client
	done    chan struct{}
	// cancel stops the supervised delivery loop. Set by Start, called
	// by Stop.
	cancel context.CancelFunc

	// StrandedAfter overrides [defaultStrandedAfter]. Tests set it low
	// so the reaper is observable; production leaves it zero.
	StrandedAfter time.Duration

	// run is the function executed inside each Hook goroutine. Tests
	// override it to exercise the goroutine plumbing (detached cancel,
	// panic recovery) without a live database. Production code leaves
	// this nil and the hook routes to [Worker.createDeliveries].
	run func(ctx context.Context, workspaceID uint32, eventType string)

	// deliverFn is the per-row delivery step. Tests override it to
	// exercise the batching — which rows may run at once, which must
	// wait — without live endpoints or a database. Production leaves it
	// nil and the batch routes to [Worker.deliver].
	deliverFn func(ctx context.Context, row generated.ClaimPendingDeliveriesRow)
}

// NewWorker creates a Worker backed by the given database.
func NewWorker(db *sql.DB, q *generated.Queries) *Worker {
	return &Worker{
		db:      db,
		queries: q,
		client:  NewSafeClient(10 * time.Second),
		done:    make(chan struct{}),
	}
}

// Hook returns an eventbus.NotifyHook that creates webhook delivery
// rows for every active subscription whose event_types list matches the
// fired event. The returned function is non-blocking: it spawns a
// goroutine so the eventbus append path is never delayed.
//
// eventInternalID is the events.id of the row that triggered this
// fan-out; the worker resolves it to events.public_id and the row's
// occurred_at, then stamps both onto each webhook_deliveries row so
// repeated dispatches of the same event collapse to a single delivery
// per subscription via the (subscription_id, event_public_id) unique
// key, and the payload's OccurredAt reflects the event's logical time
// rather than the dispatch instant.
//
// The spawned goroutine inherits the parent context's values via
// [context.WithoutCancel] so trace span / logger attributes survive
// request cancellation while DB inserts still get to finish. A
// recover() guards the goroutine: a panic inside createDeliveries
// would otherwise crash the whole flow-api process.
func (w *Worker) Hook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		detached := context.WithoutCancel(ctx)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(detached, "webhook hook panic",
						slog.Any("recover", r),
						slog.Uint64("workspace_id", uint64(workspaceID)),
						slog.String("event_type", eventType))
				}
			}()
			if w.run != nil {
				w.run(detached, workspaceID, eventType)
				return
			}
			w.createDeliveries(detached, workspaceID, eventType, eventInternalID)
		}()
	}
}

// createDeliveries finds active subscriptions for the workspace, filters
// by event type, and inserts a pending delivery row for each match.
// eventInternalID is resolved once to (event_public_id, occurred_at) so
// every delivery row in this fan-out shares the same dedupe key and the
// payload's OccurredAt is the event's logical time, not the dispatch time.
func (w *Worker) createDeliveries(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	subs, err := w.queries.ListActiveSubscriptionsForEvent(ctx, workspaceID)
	if err != nil {
		slog.Warn("webhook: failed to list active subscriptions",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_type", eventType),
			slog.String("err", err.Error()))
		return
	}

	eventRow, err := w.queries.GetEventPublicIDAndOccurredAt(ctx, generated.GetEventPublicIDAndOccurredAtParams{
		WorkspaceID: workspaceID,
		ID:          eventInternalID,
	})
	if err != nil {
		slog.Warn("webhook: failed to resolve event public id",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_type", eventType),
			slog.Uint64("event_internal_id", eventInternalID),
			slog.String("err", err.Error()))
		return
	}
	eventPubID := eventRow.PublicID
	occurredAt := eventRow.OccurredAt

	ectx := w.resolveEventContext(ctx, workspaceID, eventInternalID)

	for _, sub := range subs {
		if !matchesEventType(sub.EventTypes, eventType) {
			continue
		}

		pubID := types.New()
		now := time.Now().UTC()
		payload := buildPayload(pubID, eventPubID, eventType, occurredAt, ectx)
		if _, err := w.queries.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
			PublicID:       pubID,
			WorkspaceID:    workspaceID,
			SubscriptionID: sub.ID,
			EventType:      eventType,
			EventPublicID:  &eventPubID,
			PayloadJson:    payload,
			NextRetryAt:    sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			slog.Warn("webhook: failed to create delivery",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.String("event_type", eventType),
				slog.String("subscription", sub.PublicID.String()),
				slog.String("err", err.Error()))
		}
	}
}

// matchesEventType checks whether eventType appears in the subscription's
// event_types JSON array. A wildcard entry "*" matches everything.
func matchesEventType(raw json.RawMessage, eventType string) bool {
	if len(raw) == 0 {
		return false
	}
	var patterns []string
	if err := json.Unmarshal(raw, &patterns); err != nil {
		return false
	}
	for _, p := range patterns {
		if p == "*" || p == eventType {
			return true
		}
	}
	return false
}

// webhookResource names one resource an event targets. Receivers use it
// to fetch the current state over the REST API; a delivery without one
// says only that something changed somewhere, which no integration can
// act on.
//
// ID is always a public_id (UUID v7). The events row stores internal
// sequential FKs, and those must never leave the process.
type webhookResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// webhookPayload is the JSON body POSTed to a subscriber.
//
// It carries identifiers only, and deliberately not the source event's
// own payload_json. That column is written by every producer in the
// codebase and has carried a share token in plaintext before; a webhook
// target is an arbitrary third-party URL chosen by a workspace member,
// so forwarding the column wholesale would turn any future producer
// mistake into an external disclosure. A receiver that holds a token
// can read whatever detail it is entitled to from the API using the
// ids below.
type webhookPayload struct {
	EventID     string            `json:"eventId"`
	EventType   string            `json:"eventType"`
	DeliveryID  string            `json:"deliveryId"`
	WorkspaceID string            `json:"workspaceId,omitempty"`
	OccurredAt  int64             `json:"occurredAt"`
	Resources   []webhookResource `json:"resources,omitempty"`
}

// eventContext holds the public ids behind an events row's internal FKs.
type eventContext struct {
	workspacePublicID types.PublicID
	taskPublicID      types.PublicID
	calendarPublicID  types.PublicID
}

// resolveEventContext translates the internal FKs on an events row into
// the public ids of the rows they point at.
//
// Both target columns are nullable and an event may set either, both
// (a scheduled item exists as a task inside a calendar) or neither, so
// the joins are outer and a zero PublicID means "this event targets no
// row of that kind". A failed lookup degrades to an empty context: the
// delivery still goes out carrying the event id, which is worth more to
// the receiver than nothing at all.
func (w *Worker) resolveEventContext(ctx context.Context, workspaceID uint32, eventInternalID uint64) eventContext {
	const q = `
		SELECT w.public_id, t.public_id, c.public_id
		FROM events e
		INNER JOIN workspaces w ON w.id = e.workspace_id
		LEFT JOIN tasks t ON t.id = e.task_id
		LEFT JOIN calendars c ON c.id = e.calendar_id
		WHERE e.workspace_id = ? AND e.id = ?
		LIMIT 1
	`
	var ectx eventContext
	if err := w.db.QueryRowContext(ctx, q, workspaceID, eventInternalID).Scan(
		&ectx.workspacePublicID, &ectx.taskPublicID, &ectx.calendarPublicID,
	); err != nil {
		slog.WarnContext(ctx, "webhook: failed to resolve event resources",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.Uint64("event_internal_id", eventInternalID),
			slog.String("err", err.Error()))
		return eventContext{}
	}
	return ectx
}

// resources lists the public ids the event targets, task first so a
// receiver that only understands one kind reads the more specific one.
func (e eventContext) resources() []webhookResource {
	var out []webhookResource
	var zero types.PublicID
	if e.taskPublicID != zero {
		out = append(out, webhookResource{Type: "task", ID: e.taskPublicID.String()})
	}
	if e.calendarPublicID != zero {
		out = append(out, webhookResource{Type: "calendar", ID: e.calendarPublicID.String()})
	}
	return out
}

// buildPayload constructs the JSON payload sent to the webhook target.
//
// occurredAt comes from the source events row so the payload reflects
// when the event happened, not when the worker happened to dispatch it.
// deliveryID is the webhook_deliveries.public_id of the row this body is
// stored on, so the payload and the X-Nodate-Delivery header agree across
// every retry of that row.
func buildPayload(deliveryID, eventPublicID types.PublicID, eventType string, occurredAt time.Time, ectx eventContext) json.RawMessage {
	var wsPublicID string
	var zero types.PublicID
	if ectx.workspacePublicID != zero {
		wsPublicID = ectx.workspacePublicID.String()
	}

	p := webhookPayload{
		EventID:     eventPublicID.String(),
		EventType:   eventType,
		DeliveryID:  deliveryID.String(),
		WorkspaceID: wsPublicID,
		OccurredAt:  occurredAt.Unix(),
		Resources:   ectx.resources(),
	}
	b, err := json.Marshal(p)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// Start launches the periodic delivery processing loop in a supervised
// background goroutine. Call [Worker.Stop] to terminate it.
//
// The loop runs under bgloop so a panic while building a delivery
// payload — bad JSON from one workspace's event, say — cannot take the
// whole process down with it.
func (w *Worker) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go bgloop.Run(runCtx, "webhook.worker", nil, w.loop)
	slog.Info("webhook worker started", slog.Duration("interval", pollInterval))
}

// Stop signals the delivery loop to exit.
//
// Cancelling the loop's context is what makes the stop clean: the
// supervisor restarts a loop that returns on its own, so signalling
// through w.done alone would look like a loop dying and be restarted
// forever after shutdown.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	close(w.done)
	slog.Info("webhook worker stopped")
}

// loop is the main ticker that periodically processes pending deliveries.
func (w *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// ProcessOnce drains one batch of pending deliveries synchronously.
// Exported solely for e2e tests that need to advance the delivery
// state machine without running the 5-second background loop.
func (w *Worker) ProcessOnce(ctx context.Context) {
	w.processBatch(ctx)
}

// ProcessOnceForSubscription is the tightly-scoped variant of ProcessOnce
// used by parallel-safe e2e tests: it only delivers rows belonging to a
// single subscription, so concurrent tests cannot accidentally consume
// each other's pending rows. It runs the same claim-then-POST state
// machine as processBatch. Production callers must use [Worker.Start]
// or [Worker.ProcessOnce] instead.
func (w *Worker) ProcessOnceForSubscription(ctx context.Context, subscriptionID uint32) {
	claimed, err := w.claimForSubscription(ctx, subscriptionID)
	if err != nil {
		slog.Warn("webhook: ProcessOnceForSubscription: claim failed",
			slog.String("err", err.Error()))
		return
	}
	for _, row := range claimed {
		w.deliver(ctx, row)
	}
}

// claimForSubscription is the subscription-scoped mirror of claimBatch:
// same FOR UPDATE SKIP LOCKED select and 'delivering' flip inside one
// short transaction, restricted to a single subscription's rows.
func (w *Worker) claimForSubscription(ctx context.Context, subscriptionID uint32) ([]generated.ClaimPendingDeliveriesRow, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		SELECT d.id, d.public_id, d.workspace_id, d.subscription_id,
		       d.event_type, d.payload_json, d.attempts, d.max_attempts,
		       ws.url, ws.secret
		FROM webhook_deliveries d
		INNER JOIN webhook_subscriptions ws ON ws.id = d.subscription_id
		WHERE d.subscription_id = ?
		  AND d.status IN ('pending', 'failed')
		  AND d.next_retry_at <= NOW()
		  AND d.attempts < d.max_attempts
		  AND d.enabled = TRUE
		ORDER BY d.next_retry_at ASC, d.id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	`
	rows, err := tx.QueryContext(ctx, q, subscriptionID, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claimed []generated.ClaimPendingDeliveriesRow
	for rows.Next() {
		var r generated.ClaimPendingDeliveriesRow
		if err := rows.Scan(
			&r.ID, &r.PublicID, &r.WorkspaceID, &r.SubscriptionID,
			&r.EventType, &r.PayloadJson, &r.Attempts, &r.MaxAttempts,
			&r.Url, &r.Secret,
		); err != nil {
			return nil, err
		}
		claimed = append(claimed, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(claimed) == 0 {
		return nil, nil
	}

	ids := make([]uint32, len(claimed))
	for i, r := range claimed {
		ids[i] = r.ID
	}
	if err := w.queries.WithTx(tx).MarkDeliveriesClaimed(ctx, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

// processBatch atomically claims a batch of pending webhook deliveries
// and POSTs them. The claim commits before any network I/O, so with N
// worker replicas each due row is delivered by exactly one replica.
func (w *Worker) processBatch(ctx context.Context) {
	w.requeueStranded(ctx)

	rows, err := w.claimBatch(ctx)
	if err != nil {
		slog.Warn("webhook: failed to claim pending deliveries",
			slog.String("err", err.Error()))
		return
	}
	w.deliverBatch(ctx, rows)
}

// deliverBatch delivers a claimed batch, one goroutine per subscription
// and at most [deliveryConcurrency] subscriptions at a time.
//
// The endpoints belong to other people and their latency is theirs, not
// ours: a subscriber whose server takes the full client timeout to
// answer used to hold the entire queue for as long as it liked, because
// the batch was delivered one row after another regardless of who they
// belonged to. Fifty rows against one dead endpoint blocked every other
// tenant for the timeout times fifty, and nothing said so — the queue
// simply drained slower than it filled.
//
// Grouping by subscription is what bounds the damage: a slow endpoint
// occupies exactly one slot no matter how many rows it has queued.
// Within a subscription the rows stay sequential, both to keep the
// order the receiver sees and to avoid answering a struggling server
// with more concurrency.
func (w *Worker) deliverBatch(ctx context.Context, rows []generated.ClaimPendingDeliveriesRow) {
	if len(rows) == 0 {
		return
	}
	deliver := w.deliverFn
	if deliver == nil {
		deliver = w.deliver
	}
	bySubscription := make(map[uint32][]generated.ClaimPendingDeliveriesRow, len(rows))
	order := make([]uint32, 0, len(rows))
	for _, row := range rows {
		if _, seen := bySubscription[row.SubscriptionID]; !seen {
			order = append(order, row.SubscriptionID)
		}
		bySubscription[row.SubscriptionID] = append(bySubscription[row.SubscriptionID], row)
	}

	sem := make(chan struct{}, deliveryConcurrency)
	var wg sync.WaitGroup
	for _, subID := range order {
		queued := bySubscription[subID]
		wg.Add(1)
		go func(queued []generated.ClaimPendingDeliveriesRow) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			for _, row := range queued {
				if ctx.Err() != nil {
					// Shutdown: leave the rest in 'delivering' for the
					// reaper rather than firing requests on the way out.
					return
				}
				deliver(ctx, row)
			}
		}(queued)
	}
	wg.Wait()
}

// RequeueStrandedForTest runs one pass of the stranded-delivery reaper.
// Exported for e2e tests that need to observe the recovery without
// waiting for the background tick; production callers get it from
// processBatch.
func (w *Worker) RequeueStrandedForTest(ctx context.Context) {
	w.requeueStranded(ctx)
}

// requeueStranded returns rows abandoned in 'delivering' to the retry
// queue. See RequeueStrandedDeliveries for why they are retried rather
// than failed.
func (w *Worker) requeueStranded(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-w.strandedAfter())
	n, err := w.queries.RequeueStrandedDeliveries(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		slog.Warn("webhook: failed to requeue stranded deliveries",
			slog.String("err", err.Error()))
		return
	}
	if n > 0 {
		slog.Warn("webhook: requeued deliveries abandoned by a previous run",
			slog.Int64("count", n),
			slog.Time("delivering_since_before", cutoff))
	}
}

// strandedAfter is how long a row may sit in 'delivering' before it is
// treated as abandoned.
func (w *Worker) strandedAfter() time.Duration {
	if w.StrandedAfter > 0 {
		return w.StrandedAfter
	}
	return defaultStrandedAfter
}

// claimBatch selects up to batchSize due deliveries with
// FOR UPDATE SKIP LOCKED and flips them to 'delivering' inside a single
// short transaction. Once committed the rows no longer match the claim
// query's status filter, so a concurrent replica scanning the queue
// skips them instead of double-delivering. The row locks are held only
// for the two statements — the HTTP POST happens after COMMIT. On any
// error the deferred rollback releases the locks and the rows stay
// pending/failed for the next tick.
func (w *Worker) claimBatch(ctx context.Context) ([]generated.ClaimPendingDeliveriesRow, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	qtx := w.queries.WithTx(tx)
	rows, err := qtx.ClaimPendingDeliveries(ctx, batchSize)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uint32, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	if err := qtx.MarkDeliveriesClaimed(ctx, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

// deliver performs the HTTP POST for a single claimed ('delivering')
// row and records the terminal outcome (delivered / failed / dead).
func (w *Worker) deliver(ctx context.Context, row generated.ClaimPendingDeliveriesRow) {
	payload := row.PayloadJson
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	// Re-read the subscription immediately before the POST. Rows are
	// claimed in batches and a batch can take minutes to drain, so a
	// subscription paused or deleted after its rows were queued would
	// otherwise keep receiving them — the one thing a subscriber has no
	// way to stop from their side. It runs ahead of the URL check so a
	// retired delivery records why it was retired rather than whatever
	// its stale destination now resolves to.
	if reason, live := w.subscriptionLive(ctx, row); !live {
		if reason != "" {
			w.markCancelled(ctx, row, reason)
		}
		return
	}

	// Re-check the stored URL before every attempt: subscriptions created
	// before the SSRF policy existed (or whose DNS now points at a
	// non-public address) must not be delivered. The safe client's dialer
	// Control re-checks the actual connect address as well, so a DNS
	// record that flips between this check and the connect (rebinding)
	// is still blocked.
	if err := ValidateURL(ctx, row.Url); err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("destination rejected: %v", err))
		return
	}

	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	signature := sign(row.Secret, timestamp, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Url, bytes.NewReader(payload))
	if err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("request build error: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, signature)
	req.Header.Set(TimestampHeader, timestamp)
	req.Header.Set("X-Nodate-Event", row.EventType)
	req.Header.Set("X-Nodate-Delivery", row.PublicID.String())

	resp, err := w.client.Do(req)
	if err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("network error: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyLen))
	bodyStr := string(body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := w.queries.MarkDeliveryDelivered(ctx, generated.MarkDeliveryDeliveredParams{
			HttpStatus:   sql.NullInt16{Int16: int16(resp.StatusCode), Valid: true},
			ResponseBody: sql.NullString{String: bodyStr, Valid: bodyStr != ""},
			ID:           row.ID,
		}); err != nil {
			slog.Warn("webhook: failed to mark delivery as delivered",
				slog.String("delivery", row.PublicID.String()),
				slog.String("err", err.Error()))
		}
		return
	}

	w.markFailed(ctx, row, resp.StatusCode, bodyStr)
}

// subscriptionLive reports whether the subscription behind a claimed
// delivery still wants it. The second return value is false when the
// POST must not happen; the first is the reason to record, empty when
// the row should simply be left for the next tick.
//
// A read error is not treated as a cancellation: a database hiccup must
// not silently discard a delivery, so the row keeps its 'delivering'
// status and the stranded reaper requeues it.
func (w *Worker) subscriptionLive(ctx context.Context, row generated.ClaimPendingDeliveriesRow) (string, bool) {
	const q = `SELECT is_active, enabled FROM webhook_subscriptions WHERE id = ? LIMIT 1`
	var isActive, enabled bool
	switch err := w.db.QueryRowContext(ctx, q, row.SubscriptionID).Scan(&isActive, &enabled); {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return "subscription no longer exists", false
	default:
		slog.WarnContext(ctx, "webhook: failed to re-check subscription before delivery",
			slog.String("delivery", row.PublicID.String()),
			slog.String("err", err.Error()))
		return "", false
	}

	switch {
	case !enabled:
		return "subscription deleted", false
	case !isActive:
		return "subscription paused", false
	}
	return "", true
}

// markCancelled retires a delivery that must not be sent because its
// subscription is gone or paused.
//
// The row is retired rather than returned to the queue. A requeued row
// stays due, so it would be re-claimed on every tick for as long as the
// pause lasts, and a workspace with a paused subscription and a large
// backlog would spend its whole batch budget re-claiming rows it is
// never allowed to send. Retiring also matches what pausing means to
// the person who pressed it: events from the pause window are not
// replayed when the subscription resumes.
//
// 'dead' is the status used because the delivery status enum has no
// dedicated cancelled state; the reason is recorded in response_body so
// the delivery list distinguishes this from an exhausted retry budget.
func (w *Worker) markCancelled(ctx context.Context, row generated.ClaimPendingDeliveriesRow, reason string) {
	if err := w.queries.MarkDeliveryDead(ctx, generated.MarkDeliveryDeadParams{
		ResponseBody: sql.NullString{String: "not delivered: " + reason, Valid: true},
		ID:           row.ID,
	}); err != nil {
		slog.WarnContext(ctx, "webhook: failed to retire a delivery for an inactive subscription",
			slog.String("delivery", row.PublicID.String()),
			slog.String("reason", reason),
			slog.String("err", err.Error()))
	}
}

// markFailed marks a delivery attempt as failed. If max retries are
// exhausted the delivery is marked dead.
func (w *Worker) markFailed(ctx context.Context, row generated.ClaimPendingDeliveriesRow, httpStatus int, responseBody string) {
	nextAttempt := int(row.Attempts) // attempts is pre-increment; current index
	httpSt := sql.NullInt16{}
	if httpStatus > 0 {
		httpSt = sql.NullInt16{Int16: int16(httpStatus), Valid: true} //#nosec G115 -- HTTP status codes are 1xx-5xx, well within int16
	}
	respBody := sql.NullString{}
	if responseBody != "" {
		respBody = sql.NullString{String: responseBody, Valid: true}
	}

	// After this attempt, total attempts = row.Attempts + 1.
	if nextAttempt >= len(retryDelays) || nextAttempt+1 >= int(row.MaxAttempts) {
		if err := w.queries.MarkDeliveryDead(ctx, generated.MarkDeliveryDeadParams{
			HttpStatus:   httpSt,
			ResponseBody: respBody,
			ID:           row.ID,
		}); err != nil {
			slog.Warn("webhook: failed to mark delivery as dead",
				slog.String("delivery", row.PublicID.String()),
				slog.String("err", err.Error()))
		}
		return
	}

	retryAt := time.Now().UTC().Add(retryDelays[nextAttempt])
	if err := w.queries.MarkDeliveryFailed(ctx, generated.MarkDeliveryFailedParams{
		HttpStatus:   httpSt,
		ResponseBody: respBody,
		NextRetryAt:  sql.NullTime{Time: retryAt, Valid: true},
		ID:           row.ID,
	}); err != nil {
		slog.Warn("webhook: failed to mark delivery as failed",
			slog.String("delivery", row.PublicID.String()),
			slog.String("err", err.Error()))
	}
}

// SignatureHeader carries the v0 HMAC of a delivery.
const SignatureHeader = "X-Nodate-Signature"

// TimestampHeader carries the instant the delivery was signed, in Unix
// seconds. It is covered by the signature, so a receiver that also
// checks it against its own clock cannot be fed a captured delivery:
// keeping the original timestamp fails the clock check, and changing it
// fails the signature.
const TimestampHeader = "X-Nodate-Timestamp"

// sign computes the signature for a webhook delivery over
//
//	base = "v0:" + timestamp + ":" + body
//	sig  = "v0=" + hex(HMAC_SHA256(secret, base))
//
// which is the same scheme this service already requires of the inbound
// webhooks it verifies. Signing the body alone made a captured delivery
// replayable forever, because nothing in the signed material said when
// it was produced.
//
// Receivers verify by recomputing the HMAC over the same base string
// and rejecting a timestamp outside their tolerated clock skew (five
// minutes is the usual figure).
func sign(secret, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(payload)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// GenerateSecret produces a cryptographically random 32-byte hex secret
// suitable for webhook subscription HMAC signing.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

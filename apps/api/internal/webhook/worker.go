// Package webhook implements the background webhook delivery worker. It
// subscribes to eventbus events via [Worker.Hook], creates pending
// delivery rows for matching subscriptions, and periodically processes
// those deliveries with HMAC-SHA256 signed POST requests. Failed
// deliveries are retried with exponential backoff up to six attempts.
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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
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
}

// NewWorker creates a Worker backed by the given database.
func NewWorker(db *sql.DB, q *generated.Queries) *Worker {
	return &Worker{
		db:      db,
		queries: q,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		done: make(chan struct{}),
	}
}

// Hook returns an eventbus.NotifyHook that creates webhook delivery
// rows for every active subscription whose event_types list matches the
// fired event. The returned function is non-blocking: it spawns a
// goroutine so the eventbus append path is never delayed.
func (w *Worker) Hook() func(ctx context.Context, workspaceID uint32, eventType string) {
	return func(ctx context.Context, workspaceID uint32, eventType string) {
		go w.createDeliveries(context.Background(), workspaceID, eventType)
	}
}

// createDeliveries finds active subscriptions for the workspace, filters
// by event type, and inserts a pending delivery row for each match.
func (w *Worker) createDeliveries(ctx context.Context, workspaceID uint32, eventType string) {
	subs, err := w.queries.ListActiveSubscriptionsForEvent(ctx, workspaceID)
	if err != nil {
		slog.Warn("webhook: failed to list active subscriptions",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_type", eventType),
			slog.String("err", err.Error()))
		return
	}

	// Fetch the latest event payload for context.
	payload := w.buildPayload(ctx, workspaceID, eventType)

	for _, sub := range subs {
		if !matchesEventType(sub.EventTypes, eventType) {
			continue
		}

		pubID := types.New()
		now := time.Now().UTC()
		if _, err := w.queries.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
			PublicID:       pubID,
			WorkspaceID:    workspaceID,
			SubscriptionID: sub.ID,
			EventType:      eventType,
			EventPublicID:  sql.NullString{},
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

// buildPayload constructs the JSON payload sent to the webhook target.
func (w *Worker) buildPayload(ctx context.Context, workspaceID uint32, eventType string) json.RawMessage {
	type webhookPayload struct {
		EventType   string `json:"eventType"`
		WorkspaceID string `json:"workspaceId,omitempty"`
		OccurredAt  int64  `json:"occurredAt"`
	}
	// Look up the workspace public id for the payload.
	var wsPublicID string
	const q = `SELECT HEX(public_id) FROM workspaces WHERE id = ? LIMIT 1`
	var hexStr string
	if err := w.db.QueryRowContext(ctx, q, workspaceID).Scan(&hexStr); err == nil {
		// Convert hex to UUID format.
		if parsed, perr := types.Parse(
			hexStr[:8] + "-" + hexStr[8:12] + "-" + hexStr[12:16] + "-" + hexStr[16:20] + "-" + hexStr[20:],
		); perr == nil {
			wsPublicID = parsed.String()
		}
	}

	p := webhookPayload{
		EventType:   eventType,
		WorkspaceID: wsPublicID,
		OccurredAt:  time.Now().Unix(),
	}
	b, err := json.Marshal(p)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// Start launches the periodic delivery processing loop in a background
// goroutine. Call [Worker.Stop] to terminate it.
func (w *Worker) Start(ctx context.Context) {
	go w.loop(ctx)
	slog.Info("webhook worker started", slog.Duration("interval", pollInterval))
}

// Stop signals the delivery loop to exit.
func (w *Worker) Stop() {
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

// processBatch fetches and delivers a batch of pending webhook deliveries.
func (w *Worker) processBatch(ctx context.Context) {
	rows, err := w.queries.FindPendingDeliveries(ctx, batchSize)
	if err != nil {
		slog.Warn("webhook: failed to find pending deliveries",
			slog.String("err", err.Error()))
		return
	}

	for _, row := range rows {
		w.deliver(ctx, row)
	}
}

// deliver performs the HTTP POST for a single delivery row.
func (w *Worker) deliver(ctx context.Context, row generated.FindPendingDeliveriesRow) {
	payload := row.PayloadJson
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	signature := sign(row.Secret, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Url, bytes.NewReader(payload))
	if err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("request build error: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodate-Signature-256", signature)
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

// markFailed marks a delivery attempt as failed. If max retries are
// exhausted the delivery is marked dead.
func (w *Worker) markFailed(ctx context.Context, row generated.FindPendingDeliveriesRow, httpStatus int, responseBody string) {
	nextAttempt := int(row.Attempts) // attempts is pre-increment; current index
	httpSt := sql.NullInt16{}
	if httpStatus > 0 {
		httpSt = sql.NullInt16{Int16: int16(httpStatus), Valid: true}
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

// sign computes the HMAC-SHA256 signature for a webhook payload.
func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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

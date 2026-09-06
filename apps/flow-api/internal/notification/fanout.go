// Package notification implements the notification fan-out service that
// subscribes to eventbus events and creates per-user notification rows
// for workspace members.
package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification/prefs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// preferenceFetchRetryDelay is the back-off applied between the first
// preference-fetch attempt and the single retry. Kept small because
// the request-detached fan-out goroutine has a budget governed by
// [defaultFanoutTimeout]; exhausting half the budget on a backoff
// would defeat the purpose of retrying.
const preferenceFetchRetryDelay = 50 * time.Millisecond

// defaultFanoutTimeout caps how long a single fan-out goroutine may run
// when no explicit timeout is configured. The detached context keeps
// the trace span and logger values from the parent request but is no
// longer subject to request cancellation, so an upper bound is needed
// to prevent a misbehaving query from leaking goroutines indefinitely.
const defaultFanoutTimeout = 30 * time.Second

// Only the in_app channel has a transport behind it. A row for email or
// push is written and then read by nothing: no worker sends it, and
// MarkNotificationDelivered has no caller, so delivered_at stays NULL for
// the lifetime of the row.
//
// That is more than an unimplemented feature, because the list queries
// carry no channel predicate. A recipient who enables email for a category
// gets two rows per event and the bell renders both, identically. The fix
// belongs on the read side — a channel filter on
// ListNotificationsForUser / …Keyset / …ForWorkspace — not here: dropping
// the row instead would throw away the delivery record the eventual sender
// is meant to consume, and would make the stored preference silently inert.

// Fanout creates per-user notification rows in response to eventbus
// events. It subscribes via [eventbus.AddNotifyHook] and, for each
// event, determines the set of recipients (workspace members minus the
// actor) and inserts a notification row for each one.
//
// Fan-out goroutines detach from the request context using
// [context.WithoutCancel] so the work is not aborted when the
// originating HTTP handler returns. Each goroutine is wrapped with
// [context.WithTimeout] (configurable, defaults to 30s) to bound the
// runaway risk and is tracked by [sync.WaitGroup] so [Fanout.Shutdown]
// can wait for in-flight work to drain at process exit.
type Fanout struct {
	db      *sql.DB
	queries *generated.Queries
	email   email.Sender

	timeout time.Duration

	wg       sync.WaitGroup
	stopMu   sync.RWMutex
	stopping bool

	// run is the function executed inside each fan-out goroutine.
	// It is overridable by tests so the goroutine plumbing
	// (detached cancel, timeout, shutdown wait) can be exercised
	// without a live database. Production code leaves this nil and
	// the hook routes to [Fanout.fanout].
	run func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64)

	// fetchPreferences is the function used to load the stored
	// (recipient, channel, muted) preference rows. Production code
	// leaves this nil and the fan-out path falls through to
	// [Fanout.queries.GetNotificationPreferencesForRecipients].
	// Tests override it to simulate transient and persistent DB errors
	// so the retry-once-then-fall-back contract can be exercised
	// without a live database.
	fetchPreferences func(ctx context.Context, params generated.GetNotificationPreferencesForRecipientsParams) ([]generated.GetNotificationPreferencesForRecipientsRow, error)

	// resolveMentionedUsers turns the public ids a mention payload names
	// into the internal ids of the workspace members behind them.
	// Production code leaves this nil and the lookup falls through to
	// [Fanout.queries.FindWorkspaceMemberUserInternalIdsByPublicIds],
	// which scopes the resolution to the workspace. Tests override it so
	// the payload-to-recipient narrowing can be exercised without a live
	// database.
	resolveMentionedUsers func(ctx context.Context, params generated.FindWorkspaceMemberUserInternalIdsByPublicIdsParams) ([]generated.FindWorkspaceMemberUserInternalIdsByPublicIdsRow, error)
}

// NewFanout creates a Fanout backed by the given database and email
// transport. When emailSender is nil or a [email.NoopSender], email
// delivery is silently skipped.
func NewFanout(db *sql.DB, q *generated.Queries, emailSender email.Sender) *Fanout {
	return &Fanout{
		db:      db,
		queries: q,
		email:   emailSender,
		timeout: defaultFanoutTimeout,
	}
}

// SetTimeout overrides the per-event fan-out timeout. Values <= 0
// reset to the package default. Safe to call once at start-up before
// any hook fires; not safe under concurrent fan-out.
func (f *Fanout) SetTimeout(d time.Duration) {
	if d <= 0 {
		f.timeout = defaultFanoutTimeout
		return
	}
	f.timeout = d
}

// Hook returns an eventbus.NotifyHook that can be passed to
// [eventbus.AddNotifyHook]. The returned function is non-blocking:
// it spawns a goroutine so the eventbus append path is never delayed
// by notification fan-out.
//
// The spawned goroutine inherits the parent context's values
// (trace span, logger, actor info) via [context.WithoutCancel] but
// is no longer cancelled when the parent request completes. A
// configurable timeout caps the maximum lifetime to prevent runaways.
//
// eventInternalID is the events.id of the row that triggered this
// fan-out. It is threaded into notifications.source_event_id so the
// (recipient, source_event, channel) unique key dedupes goroutine
// retries and replicated dispatches.
func (f *Fanout) Hook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		// Refuse to start new work after Shutdown has been called.
		f.stopMu.RLock()
		if f.stopping {
			f.stopMu.RUnlock()
			return
		}
		f.wg.Add(1)
		f.stopMu.RUnlock()

		// Detach cancellation from the request context but keep the
		// values (trace span, slog attributes, etc.).
		detached := context.WithoutCancel(ctx)

		go func() {
			defer f.wg.Done()
			runCtx, cancel := context.WithTimeout(detached, f.timeout)
			defer cancel()
			fn := f.run
			if fn == nil {
				fn = f.fanout
			}
			fn(runCtx, workspaceID, eventType, eventInternalID)
		}()
	}
}

// Shutdown waits for all in-flight fan-out goroutines to finish or
// for the supplied context to be cancelled, whichever happens first.
// After Shutdown returns, new events delivered through the hook are
// dropped (a debug log line records the drop).
//
// It is safe to call Shutdown multiple times; subsequent calls only
// wait, they do not toggle state.
func (f *Fanout) Shutdown(ctx context.Context) error {
	f.stopMu.Lock()
	f.stopping = true
	f.stopMu.Unlock()

	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fanout performs the actual work of creating notification rows. It
// runs in a background goroutine and logs errors instead of returning
// them.
//
// eventInternalID anchors the at-least-once dedupe contract: the
// underlying INSERT IGNORE collides on
// (recipient_user_id, source_event_id, channel), so re-firing the
// same hook (e.g. retry, replicated dispatch) yields zero new rows.
func (f *Fanout) fanout(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	// Only fan out for event types that warrant user notifications.
	title, resourceType, severity := classifyEvent(eventType)
	if title == "" {
		return
	}

	// Resolve actor + resource for the exact event row identified by
	// eventInternalID. Anchoring on events.id (rather than "latest of
	// type") removes the race where two same-type events for the same
	// workspace land back-to-back and the second hook reads the first
	// hook's row.
	row, err := f.eventByID(ctx, workspaceID, eventInternalID)
	if err != nil {
		slog.ErrorContext(ctx, "notification fanout: failed to fetch event by id",
			slog.String("event_type", eventType),
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.Uint64("event_id", eventInternalID),
			slog.String("err", err.Error()))
		return
	}

	// Determine recipients. For an event that names a task, "everyone in
	// the workspace" is the wrong set: a notification carries the task's
	// title, so closing the list endpoints while leaving this open moves
	// the same leak to the notification bell. Recipients are therefore
	// the members who may read that task, by the same Layer 4 rule the
	// lists apply.
	var recipients []uint32
	if row.taskID.Valid {
		recipients, err = f.taskVisibleMemberUserIDs(ctx, workspaceID, uint32(row.taskID.Int64)) //#nosec G115 -- events.task_id references tasks.id, which fits uint32 within realistic deployments
	} else {
		recipients, err = f.workspaceMemberUserIDs(ctx, workspaceID)
	}
	if err != nil {
		slog.ErrorContext(ctx, "notification fanout: failed to list recipients",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.Bool("task_scoped", row.taskID.Valid),
			slog.String("err", err.Error()))
		return
	}

	// A mention is addressed, not announced. Narrowing the recipients to
	// the users the payload names keeps both properties the kind needs:
	// nobody else is told, and a mention of someone who may not read the
	// task delivers nothing, because that person was never in the set the
	// visibility rule produced. An empty result is a correct outcome.
	if eventbus.Kind(eventType) == eventbus.MentionCreated {
		recipients, err = f.mentionRecipients(ctx, workspaceID, recipients, row.payloadJSON)
		if err != nil {
			slog.ErrorContext(ctx, "notification fanout: failed to resolve mentioned users",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("event_id", eventInternalID),
				slog.String("err", err.Error()))
			return
		}
	}

	actorUserID := uint32(0)
	if row.actorUserID.Valid {
		actorUserID = uint32(row.actorUserID.Int32) //#nosec G115 -- actor_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	}

	// Filter out the actor up front so the preference batch fetch is
	// not done over a recipient set the caller will discard anyway.
	filtered := recipients[:0:0]
	for _, id := range recipients {
		if id != actorUserID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		// Recipient set is empty (only the actor was in the workspace,
		// or the actor is the sole member). Log at debug so production
		// dashboards stay quiet but tests can opt in by raising the
		// level.
		slog.DebugContext(ctx, "notification fanout: no recipients after filter",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.Uint64("event_id", eventInternalID),
			slog.Int("members_total", len(recipients)),
			slog.Uint64("actor_user_id", uint64(actorUserID)))
		return
	}

	// Load the stored preference rows for this category in a single
	// round trip; muted rows come back too, because it is the presence
	// of a mute that has to override the default. The first failure
	// triggers one retry with a small back-off; if it still fails we
	// increment the preference-fetch error counter and fall back to the
	// defaults for every recipient rather than silently dropping
	// notifications.
	eventCategory := categoryForEventType(eventType)
	prefRows, err := f.loadPreferencesWithRetry(ctx, generated.GetNotificationPreferencesForRecipientsParams{
		WorkspaceID:   workspaceID,
		EventCategory: eventCategory,
		UserIds:       filtered,
	})
	if err != nil {
		slog.ErrorContext(ctx, "notification fanout: failed to load notification preferences after retry",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_category", eventCategory),
			slog.String("event_type", eventType),
			slog.String("err", err.Error()))
		obs.IncNotificationFanoutPreferenceFetchError(err)
		prefRows = nil
	}

	// prefsByUser[recipientID] is that recipient's stored rows for this
	// category. Recipients with no rows are absent from the map and
	// [prefs.ResolveChannels] hands them the bare defaults.
	prefsByUser := make(map[uint32][]prefs.Pref, len(filtered))
	for _, p := range prefRows {
		prefsByUser[p.UserID] = append(prefsByUser[p.UserID], prefs.Pref{
			Channel: generated.NotificationsChannel(p.Channel),
			Muted:   p.IsMuted,
		})
	}

	sourceEventID := sql.NullInt64{}
	if eventInternalID != 0 {
		sourceEventID = sql.NullInt64{Int64: int64(eventInternalID), Valid: true} //#nosec G115 -- events.id is BIGINT UNSIGNED, but database/sql carries the nullable column as NullInt64; auto-increment ids stay far below int64 max
	}

	actorID := sql.NullInt32{}
	if row.actorUserID.Valid {
		actorID = row.actorUserID
	}

	// Collect every (recipient, channel) row this event produces, then
	// write them in as few statements as possible.
	//
	// One INSERT per pair meant one network round trip per pair: a
	// hundred-member workspace with two channels each spent two hundred
	// sequential round trips inside a goroutine holding a connection,
	// for one task comment. Under a burst of events the fan-out
	// goroutines pile up and the pool they share is what runs out — the
	// symptom appears in unrelated request handlers, not here.
	rows := make([]notificationRow, 0, len(filtered))
	for _, recipientID := range filtered {
		// A recipient who muted every channel of this category
		// resolves to no channels and therefore to no rows — that is
		// the whole point of the setting, so it is not an error case.
		for _, channel := range prefs.ResolveChannels(prefsByUser[recipientID]) {
			rows = append(rows, notificationRow{
				publicID:    types.New(),
				recipientID: recipientID,
				channel:     channel,
			})
		}
	}

	f.insertNotifications(ctx, notificationBatch{
		rows:             rows,
		workspaceID:      workspaceID,
		actorID:          actorID,
		sourceEventID:    sourceEventID,
		eventType:        eventType,
		resourceType:     resourceType,
		resourcePublicID: row.resourcePublicID,
		title:            title,
		severity:         severity,
		eventInternalID:  eventInternalID,
	})
}

// DeliverCalendarReminder creates one in-app notification per recipient
// for a calendar event reminder. sourceEventID is the events.id of the
// calendar.reminder row the scheduler appended when it claimed the
// reminder; threading it into notifications.source_event_id arms the
// (recipient_user_id, source_event_id, channel) unique key so a
// concurrent or replayed fan-out of the same reminder collides on the
// INSERT IGNORE instead of producing duplicate rows. A zero
// sourceEventID falls back to NULL (no dedupe), preserved only for
// callers that genuinely have no event row.
//
// Returns an error when any insert fails so the caller (the scheduler)
// can decide whether to keep the reminder claimed. Errors for individual
// recipients are logged and aggregated into the returned error; the
// method does not short-circuit on the first failure so that a transient
// problem with one row does not silently skip the rest.
func (f *Fanout) DeliverCalendarReminder(
	ctx context.Context,
	workspaceID uint32,
	eventPublicID types.PublicID,
	title string,
	recipientUserIDs []uint32,
	sourceEventID int64,
) error {
	if len(recipientUserIDs) == 0 {
		return nil
	}

	srcEventID := sql.NullInt64{}
	if sourceEventID != 0 {
		srcEventID = sql.NullInt64{Int64: sourceEventID, Valid: true}
	}

	var firstErr error
	for _, recipientID := range recipientUserIDs {
		affected, err := f.queries.CreateNotification(ctx, generated.CreateNotificationParams{
			PublicID:         types.New(),
			WorkspaceID:      workspaceID,
			RecipientUserID:  recipientID,
			ActorUserID:      sql.NullInt32{},
			SourceEventID:    srcEventID,
			EventType:        "calendar.reminder",
			ResourceType:     "calendar_event",
			ResourcePublicID: eventPublicID,
			Title:            title,
			Body:             sql.NullString{},
			Severity:         generated.NotificationsSeverityNormal,
			Channel:          generated.NotificationsChannelInApp,
		})
		if err != nil {
			slog.ErrorContext(ctx, "calendar reminder fanout: failed to create notification",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("recipient_user_id", uint64(recipientID)),
				slog.String("event_public_id", eventPublicID.String()),
				slog.String("err", err.Error()))
			if firstErr == nil {
				firstErr = fmt.Errorf("DeliverCalendarReminder: recipient=%d: %w", recipientID, err)
			}
			continue
		}
		if affected == 0 {
			// The INSERT IGNORE collided with the (recipient,
			// source_event, channel) unique key — a concurrent or
			// replayed fan-out already delivered this reminder to this
			// recipient. At-least-once happy path; count it so
			// dashboards can watch the dedup rate.
			slog.DebugContext(ctx, "calendar reminder fanout: deduplicated",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("recipient_user_id", uint64(recipientID)),
				slog.String("event_public_id", eventPublicID.String()),
				slog.Int64("source_event_id", sourceEventID))
			obs.IncNotificationFanoutDedup("unique_collision")
		}
	}
	return firstErr
}

// loadPreferencesWithRetry fetches the stored preference rows for the
// given workspace+category+recipient set. It performs at most one retry
// with a small delay before the caller falls back to the channel
// defaults. The intent is to ride out single-packet drops or short
// connection-pool stalls without giving up correctness on the first
// hiccup.
//
// Tests inject a synthetic [Fanout.fetchPreferences] hook to
// simulate transient and persistent failures without a live DB.
func (f *Fanout) loadPreferencesWithRetry(
	ctx context.Context,
	params generated.GetNotificationPreferencesForRecipientsParams,
) ([]generated.GetNotificationPreferencesForRecipientsRow, error) {
	fetch := f.fetchPreferences
	if fetch == nil {
		fetch = f.queries.GetNotificationPreferencesForRecipients
	}

	rows, err := fetch(ctx, params)
	if err == nil {
		return rows, nil
	}
	firstErr := err

	slog.WarnContext(ctx, "notification fanout: preference fetch failed, retrying once",
		slog.Uint64("workspace_id", uint64(params.WorkspaceID)),
		slog.String("event_category", params.EventCategory),
		slog.String("err", err.Error()))

	// Sleep with the same context so callers can short-circuit the
	// retry when the goroutine timeout fires.
	select {
	case <-time.After(preferenceFetchRetryDelay):
	case <-ctx.Done():
		return nil, firstErr
	}

	rows, retryErr := fetch(ctx, params)
	if retryErr == nil {
		return rows, nil
	}
	return nil, retryErr
}

// eventRow is a minimal representation of an event extracted from the
// events table for fan-out enrichment.
type eventRow struct {
	actorUserID      sql.NullInt32
	resourcePublicID types.PublicID
	// taskID is the internal id of the task the event names, when it
	// names one. It decides the recipient set: an event about a task
	// may only notify people who may read that task.
	taskID sql.NullInt64
	// payloadJSON is the event's own payload. It carries public ids
	// only — eventlog.ValidatePayloadIDs refuses an append whose payload
	// names an internal id — so anything read from it has to be resolved
	// through a workspace-scoped lookup before it means a user.
	payloadJSON json.RawMessage
}

// eventByID fetches the row identified by (workspaceID, eventInternalID).
// The workspace_id predicate is defence-in-depth — events.id is globally
// unique already, but anchoring on workspace prevents cross-tenant reads
// if a caller ever passes a stale id.
func (f *Fanout) eventByID(ctx context.Context, workspaceID uint32, eventInternalID uint64) (eventRow, error) {
	const q = `
		SELECT e.actor_user_id,
		       CASE
		         WHEN e.task_id IS NOT NULL THEN (SELECT t.public_id FROM tasks t WHERE t.id = e.task_id)
		         ELSE NULL
		       END AS resource_public_id,
		       e.task_id,
		       e.payload_json
		FROM events e
		WHERE e.id = ?
		  AND e.workspace_id = ?
		LIMIT 1
	`
	var r eventRow
	err := f.db.QueryRowContext(ctx, q, eventInternalID, workspaceID).Scan(
		&r.actorUserID,
		&r.resourcePublicID,
		&r.taskID,
		&r.payloadJSON,
	)
	if err != nil {
		return r, fmt.Errorf("eventByID: %w", err)
	}
	return r, nil
}

// mentionPayload is the part of a mention.created payload the fan-out
// reads. The ids are the public UUIDs of the users the body named.
type mentionPayload struct {
	MentionedUserIDs []string `json:"mentionedUserIds"`
}

// mentionedUserIDs resolves the users a mention payload names into their
// internal ids, scoped to the workspace the event belongs to.
//
// The resolution is the workspace-scoped one the extractor used, never a
// global lookup by public id: a payload id that belongs to another tenant,
// to a former member, or to nobody at all has to come back absent, and it
// is the membership join that makes all three answer the same way.
func (f *Fanout) mentionedUserIDs(ctx context.Context, workspaceID uint32, payload json.RawMessage) ([]uint32, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var p mentionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("mentionedUserIDs: parse payload: %w", err)
	}

	publicIDs := make([]types.PublicID, 0, len(p.MentionedUserIDs))
	for _, raw := range p.MentionedUserIDs {
		id, err := types.Parse(raw)
		if err != nil {
			// A string that is not a UUID names nobody, which is the
			// answer the lookup below gives for an id that is not a
			// member. Treating the two the same keeps a malformed
			// payload from suppressing the mentions beside it.
			continue
		}
		publicIDs = append(publicIDs, id)
	}
	if len(publicIDs) == 0 {
		return nil, nil
	}

	resolve := f.resolveMentionedUsers
	if resolve == nil {
		resolve = f.queries.FindWorkspaceMemberUserInternalIdsByPublicIds
	}
	rows, err := resolve(ctx, generated.FindWorkspaceMemberUserInternalIdsByPublicIdsParams{
		WorkspaceID: workspaceID,
		PublicIds:   publicIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("mentionedUserIDs: resolve public ids: %w", err)
	}

	ids := make([]uint32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// mentionRecipients narrows a mention's recipients to the users its
// payload names, preserving the recipient order.
//
// Only the recipient set shrinks. It is already the answer to "who may see
// this", so a named id that is absent from it belongs to someone the event
// was never allowed to reach, and the intersection is what says so without
// a second visibility rule to keep in step with the first.
func (f *Fanout) mentionRecipients(
	ctx context.Context,
	workspaceID uint32,
	recipients []uint32,
	payload json.RawMessage,
) ([]uint32, error) {
	named, err := f.mentionedUserIDs(ctx, workspaceID, payload)
	if err != nil {
		return nil, err
	}
	if len(named) == 0 {
		return nil, nil
	}
	wanted := make(map[uint32]struct{}, len(named))
	for _, id := range named {
		wanted[id] = struct{}{}
	}
	out := recipients[:0:0]
	for _, id := range recipients {
		if _, ok := wanted[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// taskVisibleMemberUserIDs returns the workspace members who may read
// the given task, which is the recipient set for any notification whose
// text is derived from it.
//
// The predicate is the Layer 4 task visibility rule turned around: the
// list queries ask "which tasks may this reader see", and this asks
// "which readers may see this task". Same four branches, same meaning —
// workspace admins and owners are elevated, public tasks are for
// everyone, a project task is for its project members, and a private
// task is for its creator and the people assigned to it.
//
// A task that has since been disabled yields no recipients, so a
// notification queued against a deleted task quietly reaches nobody
// rather than fanning out on stale rows.
func (f *Fanout) taskVisibleMemberUserIDs(ctx context.Context, workspaceID, taskID uint32) ([]uint32, error) {
	const q = `
		SELECT wm.user_id
		FROM workspace_members wm
		INNER JOIN tasks t
		  ON t.id = ?
		 AND t.workspace_id = wm.workspace_id
		 AND t.enabled = TRUE
		WHERE wm.workspace_id = ?
		  AND wm.enabled = TRUE
		  AND (
		    wm.role IN ('admin', 'owner')
		    OR t.visibility = 'public'
		    OR (t.visibility = 'project' AND EXISTS (
		      SELECT 1 FROM project_members pm
		      WHERE pm.project_id = t.project_id
		        AND pm.user_id = wm.user_id
		        AND pm.enabled = TRUE
		    ))
		    OR (t.visibility = 'private' AND (
		      t.created_by_user_id = wm.user_id
		      OR EXISTS (
		        SELECT 1 FROM task_actors ta
		        WHERE ta.task_id = t.id
		          AND ta.kind = 'user'
		          AND ta.user_id = wm.user_id
		          AND ta.enabled = TRUE
		      )
		    ))
		  )
	`
	rows, err := f.db.QueryContext(ctx, q, taskID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("taskVisibleMemberUserIDs: %w", err)
	}
	defer rows.Close()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// workspaceMemberUserIDs returns the internal user IDs of all active
// members in a workspace.
func (f *Fanout) workspaceMemberUserIDs(ctx context.Context, workspaceID uint32) ([]uint32, error) {
	const q = `
		SELECT user_id
		FROM workspace_members
		WHERE workspace_id = ?
		  AND enabled = TRUE
	`
	rows, err := f.db.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspaceMemberUserIDs: %w", err)
	}
	defer rows.Close()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// classification is what a notification row says about one event kind.
// A zero Title means the kind notifies nobody.
type classification struct {
	Title        string
	ResourceType string
	Severity     generated.NotificationsSeverity
}

// silent is the classification of a kind that deliberately notifies
// nobody. Written out rather than left as a missing map entry, because
// "nothing to say about this" and "nobody has decided yet" are different
// answers and the table has to tell them apart.
var silent = classification{}

// notify builds the classification of a kind that does notify.
func notify(title, resourceType string, severity generated.NotificationsSeverity) classification {
	return classification{Title: title, ResourceType: resourceType, Severity: severity}
}

// classifications maps every declared event kind onto its notification
// copy. It is total over [eventbus.Kinds]: a kind with no entry fails
// TestEveryKindIsClassified rather than reaching fan-out and being
// dropped by a default branch.
//
// That default branch is what this table replaces. It answered "do not
// notify" for a kind nobody had classified yet, so adding an event kind
// — or renaming one — produced a feature that worked everywhere except
// the notification bell, with nothing anywhere to say so. Most kinds
// here are [silent], and that is fine; what matters is that each one was
// looked at.
var classifications = map[eventbus.Kind]classification{
	eventbus.TaskCreated:  notify("A new task was created", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskUpdated:  notify("A task was updated", "task", generated.NotificationsSeverityLow),
	eventbus.TaskDisabled: notify("A task was deleted", "task", generated.NotificationsSeverityNormal),

	eventbus.TaskCommentAdded:   notify("A new comment was added", "comment", generated.NotificationsSeverityNormal),
	eventbus.TaskCommentEdited:  notify("A comment was edited", "comment", generated.NotificationsSeverityLow),
	eventbus.TaskCommentRemoved: notify("A comment was removed", "comment", generated.NotificationsSeverityLow),

	eventbus.TaskActorAdded:   notify("You were added to a task", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskActorRemoved: notify("You were removed from a task", "task", generated.NotificationsSeverityNormal),

	eventbus.TaskTransitionStart:    notify("A task was started", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionComplete: notify("A task was completed", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionBlock:    notify("A task was blocked", "task", generated.NotificationsSeverityHigh),
	eventbus.TaskTransitionUnblock:  notify("A task was unblocked", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionSubmit:   notify("A task was submitted for review", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionReopen:   notify("A task was reopened", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionCancel:   notify("A task was cancelled", "task", generated.NotificationsSeverityNormal),

	// itemkit kinds: task ↔ calendar_event atomic mutations. The reader
	// for these events is the "item" (task + its projections);
	// resourceType stays "task" because downstream routing is the same.
	eventbus.ItemScheduled:            notify("An item was placed on a calendar", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemUnscheduled:          notify("An item was removed from a calendar", "task", generated.NotificationsSeverityLow),
	eventbus.ItemRescheduled:          notify("An item was rescheduled", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemRenamed:              notify("An item was renamed", "task", generated.NotificationsSeverityLow),
	eventbus.ItemDeleted:              notify("An item was deleted", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemReconciled:           notify("An item was auto-reconciled", "task", generated.NotificationsSeverityLow),
	eventbus.ItemActorAdded:           notify("You were added to an item", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemActorRemoved:         notify("You were removed from an item", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemVisibilityChanged:    notify("An item's visibility changed", "task", generated.NotificationsSeverityLow),
	eventbus.ItemMilestoneLinkAdded:   notify("An item was linked to a milestone", "task", generated.NotificationsSeverityLow),
	eventbus.ItemMilestoneLinkRemoved: notify("An item was unlinked from a milestone", "task", generated.NotificationsSeverityLow),

	// agent.task.* events: AI agent lifecycle on tasks. handoff_to_user is
	// the user-facing one that the inbox badge must surface; thought is
	// silent because it records private agent reasoning.
	eventbus.AgentTaskHandoffToUser:  notify("An agent handed back to you", "task", generated.NotificationsSeverityHigh),
	eventbus.AgentTaskHandoffToAgent: notify("A task was handed off to an agent", "task", generated.NotificationsSeverityNormal),
	eventbus.AgentTaskAttached:       notify("An agent was attached to a task", "task", generated.NotificationsSeverityLow),
	eventbus.AgentTaskDetached:       notify("An agent was detached from a task", "task", generated.NotificationsSeverityLow),
	eventbus.AgentTaskThought:        silent,

	// Task metadata: the task itself carries the change and the lists
	// refresh off the stream, so a per-recipient row would be noise.
	eventbus.TaskAttachmentAdded:   silent,
	eventbus.TaskAttachmentRemoved: silent,
	eventbus.TaskDependencyAdded:   silent,
	eventbus.TaskDependencyRemoved: silent,
	eventbus.TaskConstraintAdded:   silent,
	eventbus.TaskConstraintRemoved: silent,
	eventbus.TaskLabelAdded:        silent,
	eventbus.TaskLabelRemoved:      silent,
	eventbus.TaskArchived:          silent,
	eventbus.TaskUnarchived:        silent,

	// Judge-driven task effects. The signal timeline is where these are
	// read; a notification would duplicate the transition row the Applier
	// also appends.
	eventbus.TaskAutoCompleted: silent,
	eventbus.TaskRetroDrafted:  silent,

	// Signal lifecycle: machine-facing audit anchors for the judge loop.
	eventbus.SignalAttached: silent,
	eventbus.SignalJudged:   silent,
	eventbus.SignalApplied:  silent,
	eventbus.SignalRejected: silent,

	// AI suggestions and agent runs surface in their own screens, which
	// the stream keeps fresh.
	eventbus.AiSuggestionProposed:  silent,
	eventbus.AiSuggestionApplied:   silent,
	eventbus.AiSuggestionDismissed: silent,
	eventbus.AiSuggestionEdited:    silent,
	// A proposal the executor has not acted on is a note on the activity
	// feed, not something to interrupt anyone with; the applied action
	// that may follow carries its own kind.
	eventbus.AiAutoActionProposed: silent,
	eventbus.AiAgentPaused:        silent,
	eventbus.AiAgentResumed:       silent,
	eventbus.AiAgentRunStarted:    silent,
	eventbus.AiAgentRunCompleted:  silent,
	eventbus.AiAgentRunFailed:     silent,

	// Workspace furniture: labels, pages, lenses, dashboards, timeboxes,
	// relations and exports are read from their own lists.
	eventbus.LabelCreated:            silent,
	eventbus.LabelUpdated:            silent,
	eventbus.LabelDisabled:           silent,
	eventbus.PageCreated:             silent,
	eventbus.PageUpdated:             silent,
	eventbus.PageDisabled:            silent,
	eventbus.PageArchived:            silent,
	eventbus.PageUnarchived:          silent,
	eventbus.LensShared:              silent,
	eventbus.LensUnshared:            silent,
	eventbus.LensArchived:            silent,
	eventbus.DashboardWidgetCreated:  silent,
	eventbus.DashboardWidgetUpdated:  silent,
	eventbus.DashboardWidgetDisabled: silent,
	eventbus.TimeboxCreated:          silent,
	eventbus.TimeboxUpdated:          silent,
	eventbus.TimeboxActivated:        silent,
	eventbus.TimeboxCompleted:        silent,
	eventbus.TimeboxTaskAdded:        silent,
	eventbus.TimeboxTaskRemoved:      silent,
	eventbus.TimeboxArchived:         silent,
	eventbus.RelationSuggested:       silent,
	eventbus.RelationAccepted:        silent,
	eventbus.RelationDismissed:       silent,
	eventbus.ExportRequested:         silent,

	// Calendar surface. Calendar changes reach their readers through the
	// calendar itself rather than the notification bell.
	eventbus.CalendarCreated:             silent,
	eventbus.CalendarUpdated:             silent,
	eventbus.CalendarDeleted:             silent,
	eventbus.CalendarSubscribed:          silent,
	eventbus.CalendarSubscriptionUpdated: silent,
	eventbus.CalEventCreated:             silent,
	eventbus.CalEventUpdated:             silent,
	eventbus.CalEventDeleted:             silent,
	eventbus.CalMemberAdded:              silent,
	eventbus.CalMemberRemoved:            silent,
	eventbus.CalMemberRoleChanged:        silent,
	eventbus.CalMemoCreated:              silent,
	eventbus.CalMemoUpdated:              silent,
	eventbus.CalMemoCompleted:            silent,
	eventbus.CalMemoDeleted:              silent,

	eventbus.CalEventCommentCreated:    silent,
	eventbus.CalEventCommentUpdated:    silent,
	eventbus.CalEventCommentDeleted:    silent,
	eventbus.CalEventAttachmentCreated: silent,
	eventbus.CalEventAttachmentDeleted: silent,
	eventbus.CalEventChecklistCreated:  silent,
	eventbus.CalEventChecklistUpdated:  silent,
	eventbus.CalEventChecklistDeleted:  silent,
	eventbus.CalEventAttendeeAdded:     silent,
	eventbus.CalEventAttendeeRemoved:   silent,
	eventbus.CalEventRsvpUpdated:       silent,
	eventbus.CalEventInviteCreated:     silent,
	eventbus.CalEventInviteRotated:     silent,
	eventbus.CalEventInviteRevoked:     silent,

	// Public shares are an owner-side administrative surface.
	eventbus.PublicShareCreated:         silent,
	eventbus.PublicShareUpdated:         silent,
	eventbus.PublicShareRotated:         silent,
	eventbus.PublicShareDeleted:         silent,
	eventbus.PublicShareEventsAttached:  silent,
	eventbus.PublicShareEventsReordered: silent,
	eventbus.PublicShareEventDetached:   silent,

	// Reactions and favourites are read from the row they are attached to.
	eventbus.ReactionAdded:   silent,
	eventbus.ReactionRemoved: silent,
	eventbus.FavoriteAdded:   silent,
	eventbus.FavoriteRemoved: silent,

	// A mention reaches the people the body names and nobody else; the
	// resource is the task the body was written on, which is the only
	// public id the event row can supply.
	eventbus.MentionCreated: notify("You were mentioned", "task", generated.NotificationsSeverityNormal),

	// Intake triage, description history and import jobs are read from
	// the screen that started them.
	eventbus.IntakeItemCreated:         silent,
	eventbus.IntakeItemAccepted:        silent,
	eventbus.IntakeItemRejected:        silent,
	eventbus.IntakeItemSnoozed:         silent,
	eventbus.IntakeItemDuplicate:       silent,
	eventbus.DescriptionVersionCreated: silent,

	eventbus.DescriptionVersionRestored: silent,
	eventbus.ImportJobCreated:           silent,
	eventbus.ImportJobCompleted:         silent,
	eventbus.ImportJobFailed:            silent,
	eventbus.ImportJobCancelled:         silent,

	// Workspace membership is handled by the invitation flow in auth-api.
	eventbus.WorkspaceMemberAdded:       silent,
	eventbus.WorkspaceMemberRemoved:     silent,
	eventbus.WorkspaceMemberRoleChanged: silent,

	// Historical rows only; nothing emits this any more.
	eventbus.CommentAddedLegacy: silent,
}

// classifyEvent maps an event type to a human-readable notification
// title template, resource type, and severity. Returns an empty title
// when the event type should not generate notifications.
//
// A kind outside [classifications] notifies nobody. That is reachable
// only for the `task.transition.<name>` kinds minted at runtime from a
// free-form transition name, which have no copy to render; every kind
// declared as a constant has an entry, and the guard test is what keeps
// it that way.
func classifyEvent(eventType string) (title string, resourceType string, severity generated.NotificationsSeverity) {
	c := classifications[eventbus.Kind(eventType)]
	return c.Title, c.ResourceType, c.Severity
}

// categoryForEventType maps an eventbus event type (the dotted string
// emitted on the bus, e.g. "task.comment.added") to the broader
// notification_preferences.event_category that users configure in their
// preferences UI.
//
// The schema groups dozens of event types into a handful of stable
// categories ([prefs.Categories] is the canonical list). New event types fall
// back to the broadest task.lifecycle bucket so fan-out keeps working
// before a more specific bucket is added — which also means every
// category this returns must appear in [prefs.Categories], or the events
// routed to it would be unmutable.
func categoryForEventType(eventType string) string {
	switch eventType {
	case "task.comment.added", "task.comment.edited", "task.comment.removed":
		return prefs.CategoryTaskComment
	case "task.actor.added", "task.actor.removed",
		"item.actor.added", "item.actor.removed",
		"mention.created":
		return prefs.CategoryTaskMention
	case "item.scheduled", "item.unscheduled", "item.rescheduled":
		return prefs.CategoryTimebox
	case "item.milestone.link.added", "item.milestone.link.removed":
		return prefs.CategoryRelation
	case "agent.task.handoff_to_user", "agent.task.handoff_to_agent",
		"agent.task.attached", "agent.task.detached", "agent.task.thought":
		return prefs.CategoryAI
	default:
		return prefs.CategoryTaskLifecycle
	}
}

// notificationRow is one (recipient, channel) pair to be written.
type notificationRow struct {
	publicID    types.PublicID
	recipientID uint32
	channel     generated.NotificationsChannel
}

// notificationBatch carries the per-event fields every row in a fan-out
// shares, alongside the rows themselves.
type notificationBatch struct {
	rows             []notificationRow
	workspaceID      uint32
	actorID          sql.NullInt32
	sourceEventID    sql.NullInt64
	eventType        string
	resourceType     string
	resourcePublicID types.PublicID
	title            string
	severity         generated.NotificationsSeverity
	eventInternalID  uint64
}

// insertChunkSize caps how many rows go into one statement. Large
// enough that ordinary workspaces are a single round trip, small enough
// that a big one does not build a multi-megabyte statement or hold row
// locks on hundreds of rows at once.
const insertChunkSize = 100

// insertNotifications writes the batch with one multi-row INSERT IGNORE
// per chunk.
//
// INSERT IGNORE keeps the at-least-once contract intact: the
// (recipient_user_id, source_event_id, channel) unique key still
// collapses a re-fired hook, and the affected-row count still tells new
// rows from deduplicated ones — per chunk now rather than per row,
// which is all the dedup metric ever used it for.
func (f *Fanout) insertNotifications(ctx context.Context, b notificationBatch) {
	if len(b.rows) == 0 {
		return
	}
	const columns = 12
	for start := 0; start < len(b.rows); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(b.rows) {
			end = len(b.rows)
		}
		chunk := b.rows[start:end]

		var sb strings.Builder
		sb.WriteString(`INSERT IGNORE INTO notifications (
			public_id, workspace_id, recipient_user_id, actor_user_id,
			source_event_id, event_type, resource_type, resource_public_id,
			title, body, severity, channel
		) VALUES `)
		args := make([]any, 0, len(chunk)*columns)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				r.publicID, b.workspaceID, r.recipientID, b.actorID,
				b.sourceEventID, b.eventType, b.resourceType, b.resourcePublicID,
				b.title, sql.NullString{}, b.severity, r.channel,
			)
		}

		// Retry on transient deadlocks. The fan-out goroutine runs in
		// auto-commit, so re-issuing the whole INSERT IGNORE is safe and
		// the unique key still dedupes across retries.
		var affected int64
		stmt := sb.String()
		err := dbretry.Do(ctx, "notification.CreateNotifications", func(ctx context.Context) error {
			res, e := f.db.ExecContext(ctx, stmt, args...)
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			affected = n
			return nil
		})
		if err != nil {
			slog.ErrorContext(ctx, "notification fanout: failed to create notifications",
				slog.Uint64("workspace_id", uint64(b.workspaceID)),
				slog.String("event_type", b.eventType),
				slog.Int("rows", len(chunk)),
				slog.Uint64("event_id", b.eventInternalID),
				slog.String("err", err.Error()))
			continue
		}
		if deduped := int64(len(chunk)) - affected; deduped > 0 {
			// The unique key collapsed rows that already existed. This
			// is the at-least-once happy path, so it stays at debug —
			// but the counter is what lets a dashboard notice a hook
			// that suddenly fires far more often than it should.
			slog.DebugContext(ctx, "notification fanout: deduplicated",
				slog.Uint64("workspace_id", uint64(b.workspaceID)),
				slog.Uint64("event_id", b.eventInternalID),
				slog.String("event_type", b.eventType),
				slog.Int64("deduplicated", deduped))
			for i := int64(0); i < deduped; i++ {
				obs.IncNotificationFanoutDedup("unique_collision")
			}
		}
	}
}

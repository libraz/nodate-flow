package calendar_event_day

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/signalwire"
)

const (
	// JobName is the stable identifier the runner uses for log fields
	// and future per-job metric labels. MUST NOT change once a deploy
	// has observed it.
	JobName = "calendar_event_day"

	// signalSource is the `source` value sent on every signal this job
	// emits. Pinned to the canonical calendar source from
	// packages/go-shared/signalwire, matching signal_kinds/calendar.yaml.
	signalSource = string(signalwire.SourceCalendar)

	// signalKind is the closed-enum kind the calendar signal_kinds
	// registry exposes for the event-day arrival tick.
	signalKind = "calendar.event_day_arrived"

	// signalSubjectType is the SignalsSubjectType enum value flow-api
	// recognises for calendar_events subjects.
	signalSubjectType = "calendar_event"

	// defaultTickInterval mirrors the runner's default cadence
	// (NF_FLOW_WORKER_TICK_INTERVAL, 60s). Used when Job.TickInterval is
	// left zero so a directly-constructed Job (e.g. in tests) still has a
	// sane fire-once window.
	defaultTickInterval = 60 * time.Second

	// defaultCatchUpWindow is the extra lookback the day window carries so
	// a short worker outage self-heals on the next tick. One day covers a
	// single missed local-day boundary; the dedupe key makes a longer
	// backfill harmless if an operator widens it.
	defaultCatchUpWindow = 26 * time.Hour
)

// Job emits one calendar.event_day_arrived signal per (workspace, event,
// event_day) tuple when an event's start_date arrives in the workspace's
// local timezone. The job is idempotent across replicas via the
// external_id dedupe key shaped as
// `calendar_event_day:<event_public_id>:<YYYY-MM-DD>` — flow-api's
// signals INSERT IGNORE on the (workspace_id, source, external_id)
// UNIQUE constraint collapses repeats.
type Job struct {
	// Scanner reads workspaces and the events whose local day arrives in
	// the current tick window per workspace. Required.
	Scanner *Scanner
	// Client posts the signal to flow-api. Required.
	Client *SignalsClient
	// Logger receives structured per-tick records. Required.
	Logger *slog.Logger
	// TickInterval is the runner cadence the scan widens into its
	// fire-once day window. It MUST match the interval the runner ticks
	// the job at; a value smaller than the real cadence would skip a day
	// boundary that landed between ticks. Defaults to defaultTickInterval
	// when non-positive.
	TickInterval time.Duration
	// CatchUpWindow extends the day window backwards so a worker outage
	// across one or more local-day boundaries self-heals: the next tick
	// after process start or after a long gap re-materialises the skipped
	// days. Steady-state ticks use only the elapsed time since the last
	// successful tick so the 26h lookback does not re-scan yesterday on
	// every minute. Defaults to defaultCatchUpWindow when non-positive.
	//
	// It also bounds how much a single tick may scan. A gap wider than
	// this is not discarded: the tick takes the oldest slice of it and
	// the cursor advances by exactly that much, so the remainder is
	// picked up by the ticks that follow. See spanSinceLast.
	CatchUpWindow time.Duration

	// Cursors records how far each workspace has been scanned. Optional:
	// defaults to an in-process store, which is what the job has always
	// used.
	Cursors CursorStore

	memoryCursorsOnce sync.Once
	memoryCursors     CursorStore
}

// New constructs a Job with its Scanner and Client wired against the
// supplied database pool and config knobs. Returns an error when any
// dependency is missing so cmd/worker can fail boot rather than ticking
// silently on a misconfigured deploy.
func New(db *sql.DB, baseURL, token, userAgent string, logger *slog.Logger) (*Job, error) {
	if db == nil {
		return nil, errors.New("calendar_event_day: db is required")
	}
	if logger == nil {
		return nil, errors.New("calendar_event_day: logger is required")
	}
	client, err := NewSignalsClient(baseURL, token, userAgent, logger)
	if err != nil {
		return nil, err
	}
	return &Job{
		Scanner:       &Scanner{DB: db, Logger: logger},
		Client:        client,
		Logger:        logger,
		TickInterval:  defaultTickInterval,
		CatchUpWindow: defaultCatchUpWindow,
	}, nil
}

// Name returns the stable runner identifier for the calendar event-day
// job.
func (j *Job) Name() string { return JobName }

// Tick performs one cycle of the scan + emit loop. `now` is the tick
// instant the runner observed (injected so the day-boundary behaviour is
// deterministic under test). The outcome is recorded on
// nf_flow_worker_calendar_event_day_ticks_total{status} and the duration
// on nf_flow_worker_calendar_event_day_tick_seconds.
//
// Status semantics (matches obs.metrics docs):
//   - "ok"      — workspaces were scanned and signal posting completed
//     (per-workspace failures are logged but do not change
//     the tick verdict).
//   - "error"   — listing workspaces failed; the runner records the tick
//     as failed and retries on the next interval.
//   - "skipped" — there are no enabled workspaces; nothing to do.
func (j *Job) Tick(ctx context.Context, now time.Time) error {
	timer := prometheus.NewTimer(obs.CalendarEventDayTickSeconds)
	defer timer.ObserveDuration()

	workspaces, err := j.Scanner.ListWorkspaces(ctx)
	if err != nil {
		obs.CalendarEventDayTicksTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("calendar_event_day: list workspaces: %w", err)
	}
	if len(workspaces) == 0 {
		obs.CalendarEventDayTicksTotal.WithLabelValues("skipped").Inc()
		j.Logger.DebugContext(ctx, "calendar_event_day: no enabled workspaces, skipping tick")
		return nil
	}

	for _, ws := range workspaces {
		span := j.spanForWorkspace(ctx, ws.ID, now)
		if span.upper.Before(now) {
			// The workspace is behind by more than one tick can scan.
			// Say so: a workspace that keeps failing walks forward one
			// slice per tick, and the only other sign of it is the
			// absence of signals nobody is looking for.
			j.Logger.InfoContext(ctx, "calendar_event_day: workspace is catching up",
				slog.String("workspace_public_id", ws.PublicID.String()),
				slog.Time("scanning_through", span.upper),
				slog.Duration("behind_by", now.Sub(span.upper)),
			)
		}
		if err := j.tickForWorkspace(ctx, ws, span); err != nil {
			// One bad workspace must not block the rest. The runner
			// metric stays "ok" because the loop made progress; per-
			// workspace failures surface via the slog stream.
			//
			// The cursor is deliberately left where it was: the days in
			// this span have not been materialised, and advancing past
			// them is what made a workspace with broken data lose them
			// permanently.
			j.Logger.WarnContext(ctx, "calendar_event_day: workspace tick failed",
				slog.Any("err", err),
				slog.Uint64("workspace_internal_id", uint64(ws.ID)),
				slog.String("workspace_public_id", ws.PublicID.String()),
			)
			continue
		}
		j.markScanned(ctx, ws, span.upper)
	}
	obs.CalendarEventDayTicksTotal.WithLabelValues("ok").Inc()
	return nil
}

// tickForWorkspace runs the scan + emit cycle for a single workspace.
// `span` is the fire-once day window this tick materialises. Only events
// whose local day arrives inside it are scanned, so a steady cadence
// emits each (event, day) once. Returned errors describe the
// workspace-level failure (event query); per-event POST failures are
// logged inside the loop and do not abort the workspace — partial
// progress is better than none.
func (j *Job) tickForWorkspace(ctx context.Context, ws Workspace, span scanSpan) error {
	events, err := j.Scanner.ListEventsForDays(ctx, ws.ID, ws.Timezone, span.upper, span.width)
	if err != nil {
		return fmt.Errorf("list arriving events: %w", err)
	}
	if len(events) == 0 {
		j.Logger.DebugContext(ctx, "calendar_event_day: no event-day arrivals in window for workspace",
			slog.String("workspace_public_id", ws.PublicID.String()),
			slog.String("timezone", ws.Timezone),
		)
		return nil
	}

	for _, eod := range events {
		// Honour ctx cancellation between events so a shutdown signal
		// during a long workspace fan-out drains promptly.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		body, err := j.buildSignalBody(ws, eod.Event, eod.Day, eod.ExpiresAtUnix)
		if err != nil {
			j.Logger.WarnContext(ctx, "calendar_event_day: build signal body failed",
				slog.Any("err", err),
				slog.String("workspace_public_id", ws.PublicID.String()),
				slog.String("event_public_id", eod.Event.PublicID.String()),
			)
			continue
		}

		if err := j.Client.PostSignal(ctx, body); err != nil {
			// SignalsClient has already logged the response detail at
			// warn; emit one structured record here so the (workspace,
			// event) attribution is captured even when the upstream
			// reply is opaque.
			j.Logger.WarnContext(ctx, "calendar_event_day: post signal failed",
				slog.Any("err", err),
				slog.String("workspace_public_id", ws.PublicID.String()),
				slog.String("event_public_id", eod.Event.PublicID.String()),
				slog.String("event_day", eod.Day),
			)
			continue
		}
	}
	return nil
}

// buildSignalBody assembles the PostSignalBody for one (workspace,
// event, day) tuple. Returns an error when the payload cannot be
// JSON-encoded; the caller treats that as a per-event skip.
func (j *Job) buildSignalBody(ws Workspace, ev Event, day string, expires int64) (PostSignalBody, error) {
	payload, err := json.Marshal(map[string]any{
		"startAt": ev.StartAt.UTC().Unix(),
		"allDay":  ev.AllDay,
		// linked_task_public_ids is intentionally omitted here: adding
		// the task_event_links lookup would put an unauthenticated JOIN
		// on every tick. Add the field alongside the prompt context
		// lookup if both paths need the same join.
	})
	if err != nil {
		return PostSignalBody{}, fmt.Errorf("marshal payload: %w", err)
	}

	externalID := dedupeKey(ev.PublicID.String(), day)

	return PostSignalBody{
		WorkspaceID: ws.PublicID.String(),
		Source:      signalSource,
		Kind:        signalKind,
		ExternalID:  externalID,
		SubjectType: signalSubjectType,
		SubjectID:   ev.PublicID.String(),
		Payload:     payload,
		ExpiresAt:   &expires,
	}, nil
}

// dedupeKey assembles the `external_id` flow-api collapses on. Format
// matches the calendar.yaml registry entry:
// `calendar_event_day:<event_public_id>:<YYYY-MM-DD>`.
func dedupeKey(eventPublicID, day string) string {
	return "calendar_event_day:" + eventPublicID + ":" + day
}

// scanSpan is the stretch of time one tick materialises for one
// workspace: the local days whose midnight falls in
// `[upper - width, upper)`. `upper` is the instant the cursor moves to
// when the tick succeeds, which is the tick time in steady state and an
// earlier instant while a workspace is catching up.
type scanSpan struct {
	upper time.Time
	width time.Duration
}

// spanForWorkspace resolves the span this tick should materialise for a
// workspace, reading the workspace's cursor.
//
// A cursor that cannot be read is treated as absent, which costs a
// catch-up-width scan and no correctness: the day-scoped external_id
// collapses anything already emitted.
func (j *Job) spanForWorkspace(ctx context.Context, workspaceID uint32, now time.Time) scanSpan {
	last, err := j.cursors().Load(ctx, workspaceID)
	if err != nil {
		j.Logger.WarnContext(ctx, "calendar_event_day: cursor load failed, scanning the full catch-up window",
			slog.Any("err", err),
			slog.Uint64("workspace_internal_id", uint64(workspaceID)),
		)
		last = time.Time{}
	}
	return j.spanSinceLast(last, now)
}

// spanSinceLast turns a cursor into the span for this tick.
//
//   - No cursor: scan the whole catch-up allowance. This is the first
//     tick after a process start, which with an in-memory store is also
//     the first tick after every deploy.
//   - Cursor within one interval: scan one interval. Steady state, and
//     the lower bound absorbs scheduler jitter.
//   - Cursor within the catch-up allowance: scan exactly the elapsed
//     time, so a short outage self-heals in one tick.
//   - Cursor older than the allowance: scan the OLDEST allowance-wide
//     slice of the gap, ending at `last + maxWindow` rather than at
//     `now`.
//
// That last case is the one that used to lose days. The window was
// clamped to the allowance but still measured backwards from `now`, and
// the cursor still jumped to `now` on success — so everything between
// the old cursor and `now - maxWindow` was skipped and never looked at
// again. A workspace that failed for longer than the allowance (broken
// timezone data, say) lost every day in the middle of the outage even
// after it recovered, and nothing downstream could tell those days from
// days on which nothing happened.
//
// Walking the gap forward one slice per tick converts a permanent hole
// into a delay: a workspace 30 days behind is current again after 30
// ticks, and the day-scoped external_id keeps the backfill idempotent.
func (j *Job) spanSinceLast(last, now time.Time) scanSpan {
	interval := j.TickInterval
	if interval <= 0 {
		interval = defaultTickInterval
	}
	catchUp := j.CatchUpWindow
	if catchUp <= 0 {
		catchUp = defaultCatchUpWindow
	}
	maxWindow := interval + catchUp

	if last.IsZero() {
		return scanSpan{upper: now, width: maxWindow}
	}

	elapsed := now.Sub(last)
	switch {
	case elapsed <= interval:
		return scanSpan{upper: now, width: interval}
	case elapsed <= maxWindow:
		return scanSpan{upper: now, width: elapsed}
	default:
		return scanSpan{upper: last.Add(maxWindow), width: maxWindow}
	}
}

// markScanned advances a workspace's cursor to the upper bound of the
// span the tick just completed — not to `now`, which would declare the
// unscanned remainder of a catch-up gap done.
func (j *Job) markScanned(ctx context.Context, ws Workspace, scannedThrough time.Time) {
	if err := j.cursors().Save(ctx, ws.ID, scannedThrough); err != nil {
		// A cursor that fails to persist costs a re-scan, not lost
		// days: the next tick reads the older value and materialises
		// the same span again, which the dedupe key collapses.
		j.Logger.WarnContext(ctx, "calendar_event_day: cursor save failed",
			slog.Any("err", err),
			slog.String("workspace_public_id", ws.PublicID.String()),
			slog.Time("scanned_through", scannedThrough),
		)
	}
}

func (j *Job) cursors() CursorStore {
	if j.Cursors != nil {
		return j.Cursors
	}
	j.memoryCursorsOnce.Do(func() {
		j.memoryCursors = NewMemoryCursorStore()
	})
	return j.memoryCursors
}

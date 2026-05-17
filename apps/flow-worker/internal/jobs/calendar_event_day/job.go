package calendar_event_day

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/obs"
)

const (
	// JobName is the stable identifier the runner uses for log fields
	// and future per-job metric labels. MUST NOT change once a deploy
	// has observed it.
	JobName = "calendar_event_day"

	// signalSource is the `source` value sent on every signal this job
	// emits. Pinned to "calendar" per signal_kinds/calendar.yaml. The
	// `signals.source` ENUM on flow-api still needs to grow this value
	// — see scanner.go top-of-package docstring.
	signalSource = "calendar"

	// signalKind is the closed-enum kind the calendar signal_kinds
	// registry exposes for the event-day arrival tick.
	signalKind = "calendar.event_day_arrived"

	// signalSubjectType is the SignalsSubjectType enum value flow-api
	// recognises for calendar_events subjects.
	signalSubjectType = "calendar_event"
)

// Job emits one calendar.event_day_arrived signal per (workspace, event,
// event_day) tuple when an event's start_date arrives in the workspace's
// local timezone. The job is idempotent across replicas via the
// external_id dedupe key shaped as
// `calendar_event_day:<event_public_id>:<YYYY-MM-DD>` — flow-api's
// signals INSERT IGNORE on the (workspace_id, source, external_id)
// UNIQUE constraint collapses repeats.
type Job struct {
	// Scanner reads workspaces and today's events per workspace.
	// Required.
	Scanner *Scanner
	// Client posts the signal to flow-api. Required.
	Client *SignalsClient
	// Logger receives structured per-tick records. Required.
	Logger *slog.Logger
	// Now returns the wall-clock instant the tick observes. Injected
	// rather than calling time.Now() directly so tests can fix the
	// day-boundary behaviour deterministically. Defaults to time.Now
	// when nil.
	Now func() time.Time
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
		Scanner: &Scanner{DB: db},
		Client:  client,
		Logger:  logger,
		Now:     time.Now,
	}, nil
}

// Name returns the stable runner identifier for the calendar event-day
// job.
func (j *Job) Name() string { return JobName }

// Tick performs one cycle of the scan + emit loop. The outcome is
// recorded on nf_flow_worker_calendar_event_day_ticks_total{status}
// and the duration on nf_flow_worker_calendar_event_day_tick_seconds.
//
// Status semantics (matches obs.metrics docs):
//   - "ok"      — workspaces were scanned and signal posting completed
//     (per-workspace failures are logged but do not change
//     the tick verdict).
//   - "error"   — listing workspaces failed; the runner records the tick
//     as failed and retries on the next interval.
//   - "skipped" — there are no enabled workspaces; nothing to do.
func (j *Job) Tick(ctx context.Context) error {
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

	now := j.now()
	for _, ws := range workspaces {
		if err := j.tickForWorkspace(ctx, ws, now); err != nil {
			// One bad workspace must not block the rest. The runner
			// metric stays "ok" because the loop made progress; per-
			// workspace failures surface via the slog stream.
			j.Logger.WarnContext(ctx, "calendar_event_day: workspace tick failed",
				slog.Any("err", err),
				slog.Uint64("workspace_internal_id", uint64(ws.ID)),
				slog.String("workspace_public_id", ws.PublicID.String()),
			)
		}
	}
	obs.CalendarEventDayTicksTotal.WithLabelValues("ok").Inc()
	return nil
}

// tickForWorkspace runs the scan + emit cycle for a single workspace.
// Returned errors describe the workspace-level failure (timezone load,
// event query); per-event POST failures are logged inside the loop and
// do not abort the workspace — partial progress is better than none.
func (j *Job) tickForWorkspace(ctx context.Context, ws Workspace, now time.Time) error {
	loc, err := time.LoadLocation(ws.Timezone)
	if err != nil {
		return fmt.Errorf("load workspace timezone %q: %w", ws.Timezone, err)
	}

	events, err := j.Scanner.ListTodayEvents(ctx, ws.ID, ws.Timezone, now)
	if err != nil {
		return fmt.Errorf("list today events: %w", err)
	}
	if len(events) == 0 {
		j.Logger.DebugContext(ctx, "calendar_event_day: no events today for workspace",
			slog.String("workspace_public_id", ws.PublicID.String()),
			slog.String("timezone", ws.Timezone),
		)
		return nil
	}

	day := eventDayString(now, loc)
	expires := endOfDayUnixSeconds(now, loc)

	for _, ev := range events {
		// Honour ctx cancellation between events so a shutdown signal
		// during a long workspace fan-out drains promptly.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		body, err := j.buildSignalBody(ws, ev, day, expires)
		if err != nil {
			j.Logger.WarnContext(ctx, "calendar_event_day: build signal body failed",
				slog.Any("err", err),
				slog.String("workspace_public_id", ws.PublicID.String()),
				slog.String("event_public_id", ev.PublicID.String()),
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
				slog.String("event_public_id", ev.PublicID.String()),
				slog.String("event_day", day),
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
		// linked_task_public_ids is intentionally omitted in W2: the
		// brief calls it out as a follow-up if the task_event_links
		// lookup does not fit in ~10 lines, and an unauthenticated
		// JOIN on every tick would dwarf the rest of the work. The
		// field can be added in a Phase 6 follow-up alongside the
		// judge prompt context build, which already needs the same
		// join.
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
// matches the W2 brief and the calendar.yaml registry entry:
// `calendar_event_day:<event_public_id>:<YYYY-MM-DD>`.
func dedupeKey(eventPublicID, day string) string {
	return "calendar_event_day:" + eventPublicID + ":" + day
}

// now resolves the injected clock, defaulting to time.Now when the
// constructor was not used. Centralised so the Tick path never reads
// time.Now() directly.
func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

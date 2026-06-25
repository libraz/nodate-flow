/**
 * ShareMonthGrid — read-only month-grid renderer for public calendar
 * shares. Shared by the branded `/share/cal/$token` page and the
 * chromeless `/embed/cal/$token` iframe route (`embed` prop trims the
 * frame to a transparent, edge-to-edge surface).
 *
 * The grid reuses the authenticated calendar's multi-day track-packing
 * algorithm via the self-contained `lib/share-month-grid.ts` port (so no
 * authenticated-only fields leak into the public bundle). Events are
 * non-interactive beyond a lightweight read-only popover (`<dialog>`)
 * that shows title / time / location / memo — there is no edit affordance.
 *
 * All visual values resolve from design tokens; see the sibling CSS
 * module. Logical properties + reduced-motion handling keep it RTL- and
 * a11y-safe.
 */

import type { components } from '@nodate-flow/sdk';
import { cx } from '@nodate-flow/ui/lib/cx';
import { ChevronLeft, ChevronRight, MapPin, X } from 'lucide-react';
import { type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  MAX_VISIBLE_TRACKS,
  type PositionedEvent,
  type WeekStart,
  buildMonthGrid,
  eventStartKey,
  isMultiDay,
  monthKeyOf,
  shiftMonthAnchor,
  todayKey,
} from './lib/share-month-grid';
import styles from './share-month-grid.module.css';

type ShareEvent = components['schemas']['PublicShareRenderEvent'];

/** Vertical metrics (rem) keeping single-day chips aligned with bars. */
const TRACK_REM = 1.25;

/** Per-kind pill accent colour token (matches the authenticated grid). */
function markerColorForKind(kind: string): string {
  switch (kind) {
    case 'block':
      return 'var(--nf-cal-block-color)';
    case 'free':
      return 'var(--nf-cal-free-color)';
    case 'milestone':
      return 'var(--nf-cal-milestone-color)';
    default:
      return 'var(--nf-cal-event-color)';
  }
}

/** Resolve the month anchor (`YYYY-MM-DD`) the grid should open on. */
function initialMonthAnchor(events: ShareEvent[], zone: string): string {
  // Prefer the month of the earliest event with a start; otherwise today.
  let earliest: string | null = null;
  for (const evt of events) {
    const key = eventStartKey(evt, zone);
    if (key && (earliest === null || key < earliest)) earliest = key;
  }
  const base = earliest ?? todayKey(zone);
  return `${monthKeyOf(base)}-01`;
}

export interface ShareMonthGridProps {
  events: ShareEvent[];
  /** Share publishing timezone (IANA). Day boundaries resolve here. */
  timezone: string;
  /** Start-of-week anchor; public shares default to Sunday. */
  weekStart?: WeekStart;
  /** Chromeless mode for iframe embedding (transparent, compact). */
  embed?: boolean;
}

/**
 * ShareMonthGrid — top-level month grid with month navigation. Holds the
 * displayed-month anchor and the read-only popover selection in local
 * state; everything else is derived during render.
 */
export default function ShareMonthGrid({
  events,
  timezone,
  weekStart = 'sun',
  embed = false,
}: ShareMonthGridProps): ReactElement {
  const { t, i18n } = useTranslation();
  const locale = i18n.language || 'en';

  const [monthAnchor, setMonthAnchor] = useState(() => initialMonthAnchor(events, timezone));
  const [selected, setSelected] = useState<ShareEvent | null>(null);

  const grid = buildMonthGrid(monthAnchor, events, timezone, weekStart);

  const monthLabel = new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  }).format(grid.monthAnchor);

  const weekdayFmt = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' });
  const weekdayLabels = grid.weekdayOrder.map((dow) => {
    // 2023-01-01 (UTC) is a Sunday; index by day-of-week.
    const d = new Date(Date.UTC(2023, 0, 1 + dow));
    return { dow, label: weekdayFmt.format(d) };
  });

  return (
    <div className={styles.root} data-embed={embed ? 'true' : 'false'}>
      <div className={styles.toolbar}>
        <p className={styles.monthLabel}>{monthLabel}</p>
        <div className={styles.navGroup}>
          <button
            type="button"
            className={styles.todayButton}
            onClick={() => setMonthAnchor(`${monthKeyOf(todayKey(timezone))}-01`)}
          >
            {t('share.grid.today')}
          </button>
          <button
            type="button"
            className={styles.navButton}
            aria-label={t('share.grid.prev_month')}
            onClick={() => setMonthAnchor((a) => shiftMonthAnchor(a, -1))}
          >
            <ChevronLeft size={18} aria-hidden="true" />
          </button>
          <button
            type="button"
            className={styles.navButton}
            aria-label={t('share.grid.next_month')}
            onClick={() => setMonthAnchor((a) => shiftMonthAnchor(a, 1))}
          >
            <ChevronRight size={18} aria-hidden="true" />
          </button>
        </div>
      </div>

      <div className={styles.frame}>
        <div className={styles.weekdayRow} aria-hidden="true">
          {weekdayLabels.map((w) => (
            <div
              key={w.dow}
              className={cx(
                styles.weekday,
                w.dow === 0 && styles['weekday--sun'],
                w.dow === 6 && styles['weekday--sat'],
              )}
            >
              {w.label}
            </div>
          ))}
        </div>

        <div role="grid" aria-label={t('share.grid.aria_label', { month: monthLabel })}>
          {grid.weeks.map((week) => (
            <ShareWeekRow
              key={week.key}
              week={week}
              events={events}
              timezone={timezone}
              onEventOpen={setSelected}
            />
          ))}
        </div>
      </div>

      {selected ? (
        <EventPopover
          event={selected}
          timezone={timezone}
          locale={locale}
          onClose={() => setSelected(null)}
        />
      ) : null}
    </div>
  );
}

interface ShareWeekRowProps {
  week: ReturnType<typeof buildMonthGrid>['weeks'][number];
  events: ShareEvent[];
  timezone: string;
  onEventOpen: (event: ShareEvent) => void;
}

function ShareWeekRow({ week, events, timezone, onEventOpen }: ShareWeekRowProps): ReactElement {
  const { t } = useTranslation();
  const positioned = week.bars;

  // Single-day events grouped by zoned date key within this week.
  const weekKeys = new Set(week.cells.map((c) => c.key));
  const singleDayMap = new Map<string, ShareEvent[]>();
  for (const evt of events) {
    if (isMultiDay(evt, timezone)) continue;
    const key = eventStartKey(evt, timezone);
    if (!key || !weekKeys.has(key)) continue;
    const arr = singleDayMap.get(key);
    if (arr) arr.push(evt);
    else singleDayMap.set(key, [evt]);
  }

  // Tracks reserved by multi-day bars per column.
  const reservedByCol = week.cells.map((_, col) => {
    const reserved = new Set<number>();
    for (const p of positioned) {
      if (col >= p.startCol && col < p.startCol + p.span) reserved.add(p.track);
    }
    return reserved;
  });

  const tracksUsed = Math.min(
    MAX_VISIBLE_TRACKS,
    positioned.reduce((max, p) => Math.max(max, p.track + 1), 0),
  );

  return (
    <div className={styles.weekRow}>
      {tracksUsed > 0 ? (
        <div className={styles.barOverlay} style={{ blockSize: `${tracksUsed * TRACK_REM}rem` }}>
          {positioned.map((p: PositionedEvent) => {
            if (p.track >= MAX_VISIBLE_TRACKS) return null;
            const insetStart = `calc(${(p.startCol * 100) / 7}% + var(--nf-space-px))`;
            const width = `calc(${(p.span * 100) / 7}% - var(--nf-space-1))`;
            return (
              <button
                key={`${p.event.id}-${p.startCol}`}
                type="button"
                className={cx(
                  styles.bar,
                  p.continuesLeft && styles['bar--clipStart'],
                  p.continuesRight && styles['bar--clipEnd'],
                )}
                style={{
                  insetInlineStart: insetStart,
                  inlineSize: width,
                  insetBlockStart: `${p.track * TRACK_REM}rem`,
                  background: `color-mix(in oklch, ${markerColorForKind(p.event.kind)} 22%, transparent)`,
                  borderInlineStartColor: markerColorForKind(p.event.kind),
                }}
                title={p.event.title}
                aria-label={t('share.grid.event_label', { title: p.event.title })}
                onClick={() => onEventOpen(p.event)}
              >
                <span className={styles.barTitle}>{p.event.title}</span>
              </button>
            );
          })}
        </div>
      ) : null}

      <div className={styles.dayCols}>
        {week.cells.map((cell, col) => {
          const reserved = reservedByCol[col] ?? new Set<number>();
          const singles = singleDayMap.get(cell.key) ?? [];

          // Place single-day chips into the tracks the bars left free.
          const slots: { track: number; evt: ShareEvent }[] = [];
          const used = new Set(reserved);
          let next = 0;
          for (const evt of singles) {
            while (used.has(next)) next++;
            slots.push({ track: next, evt });
            used.add(next);
            next++;
          }

          const totalItems = reserved.size + singles.length;
          const shownTracks = Math.min(MAX_VISIBLE_TRACKS, tracksUsed);
          const overflow = Math.max(0, totalItems - shownTracks);

          const trackCells = Array.from({ length: shownTracks }, (_, track) => {
            if (reserved.has(track)) {
              return { kind: 'spacer' as const, key: `${cell.key}-bar-${track}` };
            }
            const slot = slots.find((s) => s.track === track);
            if (!slot) return { kind: 'spacer' as const, key: `${cell.key}-gap-${track}` };
            return { kind: 'chip' as const, key: slot.evt.id, evt: slot.evt };
          });

          return (
            <div
              key={cell.key}
              role="gridcell"
              className={cx(styles.dayCol, !cell.inMonth && styles['dayCol--outside'])}
              data-cell-key={cell.key}
            >
              <span className={cx(styles.dayHead, cell.isToday && styles['dayHead--today'])}>
                <span
                  className={cx(
                    styles.dayNumber,
                    cell.dow === 0 && styles['dayNumber--sun'],
                    cell.dow === 6 && styles['dayNumber--sat'],
                    cell.isToday && styles['dayNumber--today'],
                  )}
                >
                  {cell.dayNumber}
                </span>
              </span>

              <div
                className={styles.trackArea}
                style={{ blockSize: `${shownTracks * TRACK_REM}rem` }}
              >
                {trackCells.map((tc) => {
                  if (tc.kind === 'spacer') {
                    return <div key={tc.key} className={styles.trackSpacer} />;
                  }
                  const evt = tc.evt;
                  return (
                    <button
                      key={tc.key}
                      type="button"
                      className={styles.chip}
                      style={{
                        background: `color-mix(in oklch, ${markerColorForKind(evt.kind)} 18%, transparent)`,
                        borderInlineStartColor: markerColorForKind(evt.kind),
                      }}
                      title={evt.title}
                      aria-label={t('share.grid.event_label', { title: evt.title })}
                      onClick={() => onEventOpen(evt)}
                    >
                      <span className={styles.chipTitle}>{evt.title}</span>
                    </button>
                  );
                })}
              </div>

              {overflow > 0 ? (
                <span className={styles.more}>{t('share.grid.more', { count: overflow })}</span>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}

interface EventPopoverProps {
  event: ShareEvent;
  timezone: string;
  locale: string;
  onClose: () => void;
}

/**
 * EventPopover — read-only `<dialog>` showing an event's title, time,
 * location and memo. Opened modally so the browser provides focus
 * trapping; ESC and backdrop click both close it. No edit affordance.
 */
function EventPopover({ event, timezone, locale, onClose }: EventPopoverProps): ReactElement {
  const { t } = useTranslation();
  const ref = useRef<HTMLDialogElement>(null);
  const zone = event.timezone || timezone;

  useEffect(() => {
    const dlg = ref.current;
    if (dlg && !dlg.open) dlg.showModal();
  }, []);

  const start = typeof event.startAt === 'number' ? new Date(event.startAt * 1000) : null;
  const end = typeof event.endAt === 'number' ? new Date(event.endAt * 1000) : null;
  const whenLabel = formatWhen({ start, end, allDay: event.allDay, zone, locale, t });

  return (
    <dialog
      ref={ref}
      className={styles.popover}
      onClose={onClose}
      onClick={(e) => {
        // Close when the backdrop (the dialog element itself) is clicked.
        if (e.target === ref.current) ref.current?.close();
      }}
      aria-label={event.title}
    >
      <div className={styles.popoverHeader}>
        <h2 className={styles.popoverTitle}>{event.title}</h2>
        <button
          type="button"
          className={styles.popoverClose}
          aria-label={t('share.grid.close')}
          onClick={() => ref.current?.close()}
        >
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <p className={styles.popoverRow}>{whenLabel}</p>
      {event.location ? (
        <p className={styles.popoverRow}>
          <MapPin size={14} aria-hidden="true" />
          {event.location}
        </p>
      ) : null}
      {event.memo ? <p className={styles.popoverMemo}>{event.memo}</p> : null}
    </dialog>
  );
}

interface FormatWhenArgs {
  start: Date | null;
  end: Date | null;
  allDay: boolean;
  zone: string;
  locale: string;
  t: (key: string) => string;
}

/**
 * formatWhen — human-readable "when" label using `Intl.DateTimeFormat`
 * pinned to the share zone so the value matches the publisher's view,
 * never the visitor's local zone. Mirrors the share page's list-card
 * formatter behaviour.
 */
function formatWhen({ start, end, allDay, zone, locale, t }: FormatWhenArgs): string {
  if (!start) return t('share.event_undated');
  const dateMedium = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeZone: zone });
  const dateTimeMedium = new Intl.DateTimeFormat(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: zone,
  });
  const timeShort = new Intl.DateTimeFormat(locale, { timeStyle: 'short', timeZone: zone });

  const safe = (fn: () => string, fallback: string): string => {
    try {
      return fn();
    } catch {
      return fallback;
    }
  };

  if (allDay) return safe(() => dateMedium.format(start), start.toISOString());
  if (end && isSameZonedDay(start, end, zone)) {
    return `${safe(() => dateTimeMedium.format(start), start.toISOString())} - ${safe(
      () => timeShort.format(end),
      end.toISOString(),
    )}`;
  }
  if (end) {
    return `${safe(() => dateTimeMedium.format(start), start.toISOString())} - ${safe(
      () => dateTimeMedium.format(end),
      end.toISOString(),
    )}`;
  }
  return safe(() => dateTimeMedium.format(start), start.toISOString());
}

function isSameZonedDay(a: Date, b: Date, zone: string): boolean {
  try {
    const fmt = new Intl.DateTimeFormat('en-CA', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone: zone,
    });
    return fmt.format(a) === fmt.format(b);
  } catch {
    return a.toDateString() === b.toDateString();
  }
}

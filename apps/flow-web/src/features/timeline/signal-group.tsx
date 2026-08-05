/**
 * SignalGroup — Phase 6 / L3 signal-grouped timeline block.
 *
 * Renders a causal cluster of events that share a common
 * `triggeredBySignalId`. The block surfaces:
 *
 *   - A header identifying the signal source + kind (derived from the
 *     `signal.attached` event payload that lives inside the group when
 *     present) plus the timestamp of the earliest member.
 *   - A judge bubble showing the LLM reasoning excerpt + confidence
 *     badge (derived from the `signal.judged` event payload if present
 *     in the group).
 *   - The grouped event list (rendered through `EventCard`).
 *   - An optional Reverse footer that targets the most recent
 *     un-reversed LLM-origin event in the group via the
 *     `events-reverse` endpoint.
 *
 * Graceful degradation: the backend Event DTO does not currently expose
 * a `signalSummary` envelope. We infer source / kind / reasoning /
 * confidence from the payloads of `signal.attached` and `signal.judged`
 * events that live inside the group. When neither carries usable data
 * the header degrades to the generic "Caused by: AI signal" label and
 * the judge bubble is hidden.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import { confirm } from '@nodate-flow/ui/primitives/confirm';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';
import { type TimelineEvent, timelineKeys } from './api';
import EventCard from './event-card';

export interface SignalGroupProps {
  /** Public id (UUID v7) of the common signal. */
  signalId: string;
  /** Workspace public id — required for the reverse endpoint. */
  workspaceId: string;
  /** All events that share `triggeredBySignalId === signalId`. */
  events: TimelineEvent[];
}

/**
 * Confidence-to-tone mapping. Mirrors the design spec thresholds:
 *   - >= 0.85 success
 *   - >= 0.60 warning
 *   - <  0.60 danger
 */
function confidenceTone(value: number): BadgeTone {
  if (value >= 0.85) return 'success';
  if (value >= 0.6) return 'warning';
  return 'danger';
}

interface SignalSummary {
  source: string | undefined;
  kind: string | undefined;
  reasoning: string | undefined;
  confidence: number | undefined;
}

/**
 * Read a string field from an unknown event payload. Returns `undefined`
 * unless the payload is an object whose property is a non-empty string.
 */
function readString(payload: unknown, key: string): string | undefined {
  if (payload === null || typeof payload !== 'object') return undefined;
  const value = (payload as Record<string, unknown>)[key];
  if (typeof value !== 'string' || value.length === 0) return undefined;
  return value;
}

/**
 * Read a numeric field from an unknown event payload. Returns
 * `undefined` unless the value is a finite number after coercion.
 */
function readNumber(payload: unknown, key: string): number | undefined {
  if (payload === null || typeof payload !== 'object') return undefined;
  const raw = (payload as Record<string, unknown>)[key];
  const n = typeof raw === 'number' ? raw : Number(raw);
  if (!Number.isFinite(n)) return undefined;
  return n;
}

/**
 * Derive a {@link SignalSummary} from the events that live inside the
 * group. The `signal.attached` event carries `{source, kind}`; the
 * `signal.judged` event carries `{reasoningExcerpt, confidence}`. The
 * derived summary is the union, with values taken from whichever event
 * in the group provides them.
 */
function deriveSummary(events: TimelineEvent[]): SignalSummary {
  let source: string | undefined;
  let kind: string | undefined;
  let reasoning: string | undefined;
  let confidence: number | undefined;
  for (const ev of events) {
    if (ev.type === 'signal.attached') {
      source ??= readString(ev.payload, 'source');
      kind ??= readString(ev.payload, 'kind');
    }
    if (ev.type === 'signal.judged') {
      reasoning ??= readString(ev.payload, 'reasoningExcerpt');
      confidence ??= readNumber(ev.payload, 'confidence');
    }
  }
  return { source, kind, reasoning, confidence };
}

/**
 * Find the latest LLM-origin event in the group that has not yet been
 * reversed. Returns `undefined` when no such event exists (either every
 * event was user-driven or everything is already reversed). The events
 * arrive ordered server-side by `occurredAt DESC`; we walk from the
 * front (newest first) and return the first match.
 */
function findReverseTarget(events: TimelineEvent[]): TimelineEvent | undefined {
  for (const ev of events) {
    const isLlmOrigin = ev.actorAgentId !== undefined && ev.actorAgentId.length > 0;
    const isReversal = ev.reversesEventId !== undefined && ev.reversesEventId.length > 0;
    if (isLlmOrigin && !isReversal && !ev.wasReversed) return ev;
  }
  return undefined;
}

/**
 * Format the group anchor timestamp. We use the earliest (latest in
 * `occurredAt DESC` order) member event so the timestamp matches the
 * "when did this signal arrive" intuition rather than the latest
 * reaction. Falls back to the first event when the group is empty
 * (defensive — caller already filtered).
 */
function formatTimestamp(events: TimelineEvent[], locale: string): string {
  const anchor = events[events.length - 1] ?? events[0];
  if (!anchor) return '';
  try {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(anchor.occurredAt * 1000));
  } catch {
    return new Date(anchor.occurredAt * 1000).toISOString();
  }
}

/**
 * Map a reverse-endpoint error to a translation key suffix. Falls back
 * to `'fetch_error'` for transport or unknown server errors.
 */
function reverseErrorKey(err: unknown): string {
  if (!(err instanceof ApiError)) return 'fetch_error';
  switch (err.code) {
    case 'AI.REVERSE.NOT_LLM_ORIGIN':
      return 'not_llm_origin';
    case 'AI.REVERSE.ALREADY_REVERSED':
      return 'already_reversed';
    case 'AI.REVERSE.TARGET_NOT_FOUND':
      return 'target_not_found';
    default:
      return 'fetch_error';
  }
}

export default function SignalGroup({
  signalId,
  workspaceId,
  events,
}: SignalGroupProps): ReactElement {
  const { t, i18n } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';
  const qc = useQueryClient();

  const summary = deriveSummary(events);
  const reverseTarget = findReverseTarget(events);

  // A group is "fully reversed" when every member event is either a
  // reversal entry or has been reversed. Carries the 0.7 opacity dim
  // per the design spec.
  const isFullyReversed =
    events.length > 0 &&
    events.every(
      (ev) => (ev.reversesEventId !== undefined && ev.reversesEventId.length > 0) || ev.wasReversed,
    );

  const reverseMut = useMutation({
    mutationFn: async (eventPublicId: string) => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/events/{eventPublicId}/reverse', {
        params: { path: { wsId: workspaceId, eventPublicId } },
      });
      if (error || !data) {
        // Funnel the response into an ApiError so `reverseErrorKey`
        // below can dispatch on the typed `code` (RFC 7807 `type`).
        throw toApiError(error, 'Failed to reverse event');
      }
      return data;
    },
    onError: (err) => {
      const key = `signal.reverse_error.${reverseErrorKey(err)}`;
      toaster.show({ tone: 'danger', message: t(key) });
    },
    onSuccess: () => {
      toaster.show({ tone: 'success', message: t('signal.reverse_success') });
    },
    onSettled: () => {
      // Invalidate every cached timeline scope — the reverse touches a
      // single workspace's events log so we cannot know which task /
      // project / workspace scope is currently mounted without
      // threading more state in. Cheap; timelines are paginated.
      void qc.invalidateQueries({ queryKey: timelineKeys.all });
    },
  });

  const handleReverseClick = async (): Promise<void> => {
    if (!reverseTarget) return;
    const ok = await confirm.ask({
      title: t('signal.reverse_confirm.title'),
      message: t('signal.reverse_confirm.body'),
      confirmLabel: t('signal.reverse_confirm.confirm'),
      cancelLabel: t('signal.reverse_confirm.cancel'),
      tone: 'danger',
    });
    if (!ok) return;
    reverseMut.mutate(reverseTarget.id);
  };

  const showReverse = reverseTarget !== undefined && !isFullyReversed;
  const sourceLabel = summary.source ?? t('signal.unknown_source');
  const causedByLabel = t('signal.caused_by', { source: sourceLabel });
  const timestamp = formatTimestamp(events, locale);
  const headerId = `nf-sig-${signalId}`;

  return (
    <article
      aria-labelledby={headerId}
      data-signal-id={signalId}
      data-reversed={isFullyReversed || undefined}
      style={{
        containerType: 'inline-size',
        margin: 'var(--nf-space-2) 0',
        padding: 'var(--nf-space-3)',
        background: 'var(--nf-color-bg-sunken)',
        border: '1px solid var(--nf-color-border)',
        borderRadius: '0.5rem',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
        opacity: isFullyReversed ? 0.7 : 1,
      }}
    >
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--nf-space-2)',
          flexWrap: 'wrap',
        }}
      >
        <span
          id={headerId}
          style={{
            color: 'var(--nf-color-fg)',
            fontSize: 'var(--nf-text-sm)',
            fontWeight: 500,
          }}
        >
          {causedByLabel}
        </span>
        {summary.kind !== undefined ? <Badge tone="info">{summary.kind}</Badge> : null}
        {isFullyReversed ? <Badge tone="neutral">{t('signal.reversed_label')}</Badge> : null}
        <span
          style={{
            marginInlineStart: 'auto',
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs)',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {timestamp}
        </span>
        {showReverse ? (
          <span data-slot="reverse-desktop" className="nf-signal-reverse-desktop">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={handleReverseClick}
              disabled={reverseMut.isPending}
            >
              {t('signal.reverse')}
            </Button>
          </span>
        ) : null}
      </header>

      {summary.reasoning !== undefined ? (
        <details
          style={{
            background: 'var(--nf-color-surface)',
            border: '1px solid var(--nf-color-hairline)',
            borderRadius: '0.375rem',
            padding: 'var(--nf-space-2) 0.625rem',
          }}
        >
          <summary
            style={{
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm)',
              listStyle: 'revert',
            }}
          >
            <span>{t('signal.reasoning')}</span>
            {summary.confidence !== undefined ? (
              <Badge tone={confidenceTone(summary.confidence)}>
                {t('signal.confidence', {
                  value: summary.confidence.toFixed(2),
                })}
              </Badge>
            ) : null}
          </summary>
          <p
            style={{
              marginBlockStart: 'var(--nf-space-2)',
              marginBlockEnd: 0,
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm)',
              lineHeight: 1.5,
              whiteSpace: 'pre-wrap',
            }}
          >
            {summary.reasoning}
          </p>
        </details>
      ) : null}

      <ol
        // biome-ignore lint/a11y/noRedundantRoles: implicit role="list" gets stripped by some screen readers when ol has list-style:none, hence explicit role
        role="list"
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1)',
        }}
      >
        {events.map((ev) => (
          <li key={ev.id} style={{ margin: 0 }}>
            <EventCard event={ev} />
          </li>
        ))}
      </ol>

      {showReverse ? (
        <footer
          data-slot="reverse-mobile"
          className="nf-signal-reverse-mobile"
          style={{ display: 'flex', justifyContent: 'flex-end' }}
        >
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleReverseClick}
            disabled={reverseMut.isPending}
          >
            {t('signal.reverse')}
          </Button>
        </footer>
      ) : null}
    </article>
  );
}

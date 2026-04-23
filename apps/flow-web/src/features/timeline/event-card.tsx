/**
 * EventCard — single timeline event row with actor avatar, translated
 * message, and relative timestamp. Payload is shown in a collapsible
 * `<details>` block when non-empty.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { TimelineEvent } from './api';

export interface EventCardProps {
  event: TimelineEvent;
}

/** Brand colors for external sources and event categories (fixed, not theme-dependent). */
const SOURCE_COLOR = {
  github: '#6e5494',
  slack: '#4a154b',
  google: '#4285f4',
  signal: '#0ea5e9',
  ai: '#10b981',
  task: '#f59e0b',
} as const;

/**
 * eventSourceTag returns a short category label derived from the event
 * type prefix. It powers the 4.WEB-1 timeline source-mix color coding
 * so every event source lives in the same feed but stays
 * distinguishable at a glance.
 */
export function eventSourceTag(
  type: string,
  payload?: unknown,
): {
  label: string;
  color: string;
} {
  // signal.attached carries the external origin inside payload.source;
  // the event type itself is namespace-only ("signal.attached").
  if (type.startsWith('signal.')) {
    const src =
      payload && typeof payload === 'object' && 'source' in payload
        ? String((payload as { source?: unknown }).source ?? '')
        : '';
    if (src === 'github') return { label: 'github', color: SOURCE_COLOR.github };
    if (src === 'slack') return { label: 'slack', color: SOURCE_COLOR.slack };
    if (src === 'google' || src === 'webhook')
      return { label: 'google', color: SOURCE_COLOR.google };
    return { label: 'signal', color: SOURCE_COLOR.signal };
  }
  if (type.startsWith('ai.') || type.startsWith('mcp.')) {
    return { label: 'ai', color: SOURCE_COLOR.ai };
  }
  if (type.startsWith('task.')) {
    return { label: 'task', color: SOURCE_COLOR.task };
  }
  return { label: 'system', color: 'var(--nf-color-fg-muted)' };
}

function formatRelative(occurredAt: number, locale: string): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
    // Server timestamps should never be in the future; clamp minor clock skew to "now"
    // so we never render "in 1 second" for an event that just happened.
    const rawDiff = occurredAt - Math.floor(Date.now() / 1000);
    const diffSec = rawDiff > 0 ? 0 : rawDiff;
    const abs = Math.abs(diffSec);
    if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
    if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
    if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
    if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
    if (abs < 31_536_000) return rtf.format(Math.round(diffSec / 2_592_000), 'month');
    return rtf.format(Math.round(diffSec / 31_536_000), 'year');
  } catch {
    return new Date(occurredAt * 1000).toISOString();
  }
}

function hasPayload(payload: unknown): boolean {
  if (payload === null || payload === undefined) return false;
  if (typeof payload === 'object') return Object.keys(payload as object).length > 0;
  return true;
}

export default function EventCard({ event }: EventCardProps): ReactElement {
  const { t, i18n } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';

  const displayName = event.actorDisplayName?.trim();
  const hasName = displayName !== undefined && displayName.length > 0;
  const actorLabel = hasName ? displayName : t('actor.system');
  // Avatar initial: first character of each whitespace-separated word, max 2.
  // Never derive initials from the raw actorUserId (UUID).
  const initials = hasName
    ? displayName
        .split(/\s+/)
        .filter((w) => w.length > 0)
        .slice(0, 2)
        .map((w) => (w[0] ?? '').toUpperCase())
        .join('') || (displayName[0] ?? '').toUpperCase()
    : t('actor.initials_fallback');

  const normalizedType = event.type.replace(/\./g, '_');
  const messageKey = `event.${normalizedType}`;
  const translated = t(messageKey, { actor: actorLabel, defaultValue: event.type });
  // Short kind label for the payload disclosure summary. Falls back to
  // the raw identifier so newly-added backend events still render (as
  // the raw dotted string) instead of a broken translation key.
  const kindLabel = t(`event_kind.${normalizedType}`, { defaultValue: event.type });

  const payloadVisible = hasPayload(event.payload);
  const tag = eventSourceTag(event.type, event.payload);

  return (
    <div
      style={{
        position: 'relative',
        display: 'grid',
        gridTemplateColumns: 'auto 1fr',
        columnGap: '0.875rem',
        paddingInlineStart: '0.25rem',
        paddingBlock: '0.625rem',
      }}
    >
      {/* rail dot */}
      <div
        aria-hidden
        style={{
          gridColumn: '1 / 2',
          gridRow: '1 / span 2',
          position: 'relative',
          inlineSize: '1.75rem',
          display: 'flex',
          justifyContent: 'center',
        }}
      >
        <div
          style={{
            inlineSize: '1.75rem',
            blockSize: '1.75rem',
            borderRadius: '999px',
            background: 'var(--nf-color-bg)',
            border: `2px solid ${tag.color}`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 1,
          }}
        >
          <Avatar alt={actorLabel} initials={initials} size="sm" />
        </div>
      </div>

      <div
        style={{
          gridColumn: '2 / 3',
          minInlineSize: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.25rem',
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'baseline',
            gap: '0.5rem',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ color: 'var(--nf-color-fg)', lineHeight: 1.4, wordBreak: 'break-word' }}>
            {translated}
          </span>
          <span
            style={{
              padding: '0 0.375rem',
              borderRadius: '0.25rem',
              border: `1px solid ${tag.color}`,
              color: tag.color,
              fontSize: '0.625rem',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}
          >
            {tag.label}
          </span>
          <span
            style={{
              marginInlineStart: 'auto',
              color: 'var(--nf-color-fg-muted)',
              fontSize: '0.75rem',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {formatRelative(event.occurredAt, locale)}
          </span>
        </div>
        {payloadVisible ? (
          <details>
            <summary
              style={{ cursor: 'pointer', color: 'var(--nf-color-fg-muted)', fontSize: '0.75rem' }}
            >
              {kindLabel}
            </summary>
            <pre
              style={{
                marginBlockStart: '0.25rem',
                padding: '0.5rem',
                background: 'var(--nf-color-surface))',
                borderRadius: '0.25rem',
                fontSize: '0.7rem',
                overflowX: 'auto',
              }}
            >
              {JSON.stringify(event.payload, null, 2)}
            </pre>
          </details>
        ) : null}
      </div>
    </div>
  );
}

/**
 * EventCard — single timeline event row with actor avatar, translated
 * message, and relative timestamp. Payload is shown in a collapsible
 * `<details>` block when non-empty.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { TimelineEvent } from './api';

export interface EventCardProps {
  event: TimelineEvent;
}

/**
 * eventSourceTag returns a short category label derived from the event
 * type prefix. It powers the 4.WEB-1 timeline source-mix color coding
 * so every event source lives in the same feed but stays
 * distinguishable at a glance.
 */
export function eventSourceTag(type: string): {
  label: string;
  color: string;
} {
  if (type.startsWith('signal.github') || type.includes('github')) {
    return { label: 'github', color: '#6e5494' };
  }
  if (type.startsWith('signal.slack') || type.includes('slack')) {
    return { label: 'slack', color: '#4a154b' };
  }
  if (type.startsWith('signal.google') || type.includes('google')) {
    return { label: 'google', color: '#4285f4' };
  }
  if (type.startsWith('signal.')) {
    return { label: 'signal', color: '#0ea5e9' };
  }
  if (type.startsWith('ai.') || type.startsWith('mcp.')) {
    return { label: 'ai', color: '#10b981' };
  }
  if (type.startsWith('task.')) {
    return { label: 'task', color: '#f59e0b' };
  }
  return { label: 'system', color: 'var(--color-muted)' };
}

function formatRelative(occurredAt: number, locale: string): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
    const diffSec = occurredAt - Math.floor(Date.now() / 1000);
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

  const messageKey = `event.${event.type.replace(/\./g, '_')}`;
  const translated = t(messageKey, { actor: actorLabel, defaultValue: event.type });

  const payloadVisible = hasPayload(event.payload);

  return (
    <Card
      style={{
        padding: '0.75rem',
        display: 'flex',
        gap: '0.75rem',
        alignItems: 'flex-start',
      }}
    >
      <div
        aria-hidden
        style={{
          inlineSize: '0.25rem',
          alignSelf: 'stretch',
          background: eventSourceTag(event.type).color,
          borderRadius: '0.125rem',
        }}
      />
      <Avatar alt={actorLabel} initials={initials} size="sm" />
      <div
        style={{
          flex: 1,
          minInlineSize: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.25rem',
        }}
      >
        <div style={{ color: 'var(--color-fg)', lineHeight: 1.4, wordBreak: 'break-word' }}>
          {translated}
        </div>
        <div
          style={{
            color: 'var(--color-muted)',
            fontSize: '0.75rem',
            display: 'flex',
            gap: '0.5rem',
            alignItems: 'center',
          }}
        >
          <span
            style={{
              padding: '0 0.375rem',
              borderRadius: '0.25rem',
              border: `1px solid ${eventSourceTag(event.type).color}`,
              color: eventSourceTag(event.type).color,
              fontSize: '0.65rem',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}
          >
            {eventSourceTag(event.type).label}
          </span>
          {formatRelative(event.occurredAt, locale)}
        </div>
        {payloadVisible ? (
          <details style={{ marginBlockStart: '0.25rem' }}>
            <summary
              style={{ cursor: 'pointer', color: 'var(--color-muted)', fontSize: '0.75rem' }}
            >
              {event.type}
            </summary>
            <pre
              style={{
                marginBlockStart: '0.25rem',
                padding: '0.5rem',
                background: 'var(--color-surface-2, var(--color-bg))',
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
    </Card>
  );
}

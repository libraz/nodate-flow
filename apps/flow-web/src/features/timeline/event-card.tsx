/**
 * EventCard — single timeline event row with actor avatar, translated
 * message, and relative timestamp. Payload is shown in a collapsible
 * `<details>` block when non-empty.
 */

import { buildAvatarUrl } from '@nodate-flow/sdk';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import type { TFunction } from 'i18next';
import { Bot } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { authApiBaseUrl } from '../../lib/sdk';
import { INTEGRATION_SOURCE_COLORS } from '../../lib/source-colors';
import type { TimelineEvent } from './api';

export interface EventCardProps {
  event: TimelineEvent;
}

/**
 * Brand and category colors used by the timeline source-mix coding.
 * Re-exposed under the original `SOURCE_COLOR` name so existing
 * call-sites in this file keep their phrasing while the actual hex
 * literals live in `lib/source-colors.ts` behind a single
 * `nf-token-override` annotation.
 */
const SOURCE_COLOR = INTEGRATION_SOURCE_COLORS;

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

/** Known `task.updated` field keys that have first-class i18n labels and
 * value formatters. Any field outside this list falls through to
 * capitalize-underscore rendering and `String(value)`. */
const KNOWN_UPDATED_FIELDS = [
  'priority',
  'status',
  'due_at',
  'start_at',
  'assignee_id',
  'estimate_minutes',
  'title',
  'description',
] as const;

type KnownUpdatedField = (typeof KNOWN_UPDATED_FIELDS)[number];

function isKnownUpdatedField(field: string): field is KnownUpdatedField {
  return (KNOWN_UPDATED_FIELDS as readonly string[]).includes(field);
}

/** Capitalize a snake_case identifier into "Snake Case" as a last-resort
 * label when no i18n key is registered for the field. */
function capitalizeSnakeCase(input: string): string {
  return input
    .split('_')
    .filter((part) => part.length > 0)
    .map((part) => {
      const first = part[0] ?? '';
      return first.toUpperCase() + part.slice(1);
    })
    .join(' ');
}

function fieldLabel(field: string, t: TFunction): string {
  if (isKnownUpdatedField(field)) {
    return t(`payload.field.${field}`, { defaultValue: capitalizeSnakeCase(field) });
  }
  return capitalizeSnakeCase(field);
}

/** Format a unix-seconds timestamp as a locale-aware medium date.
 * Returns em-dash for null/undefined and raw `String(value)` when the
 * value can't be coerced to a finite number. */
function formatUnixDate(value: unknown, locale: string, unassigned: string): string {
  if (value === null || value === undefined) return unassigned;
  const n = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(n)) return String(value);
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(n * 1000));
  } catch {
    return new Date(n * 1000).toISOString();
  }
}

/** Map task priority integer (0..4) to the `common:tasks.priority.<name>`
 * i18n key suffix. Mirrors the priority enum shared across the tasks
 * feature (see task-spreadsheet-view priority select options). */
const PRIORITY_NAMES = ['none', 'low', 'medium', 'high', 'urgent'] as const;

function formatPriorityValue(value: unknown, t: TFunction): string {
  const n = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(n)) return String(value);
  const idx = Math.trunc(n);
  const name = PRIORITY_NAMES[idx];
  if (name === undefined) return String(value);
  // Priority labels are maintained under the `common` namespace alongside
  // the task detail/spreadsheet pickers; reuse them rather than duplicating.
  return t(`tasks.priority.${name}`, { ns: 'common', defaultValue: String(value) });
}

function formatFieldValue(
  field: string,
  value: unknown,
  locale: string,
  unassigned: string,
  t: TFunction,
): string {
  if (value === null || value === undefined) return unassigned;
  switch (field) {
    case 'priority':
      return formatPriorityValue(value, t);
    case 'estimate_minutes':
      return String(value);
    case 'due_at':
    case 'start_at':
      return formatUnixDate(value, locale, unassigned);
    // TODO: resolve assignee_id to a member display name once a
    // workspace-member DTO is wired into the timeline feature.
    case 'assignee_id':
      return String(value);
    case 'title':
    case 'description':
    case 'status':
      return `"${String(value)}"`;
    default:
      return String(value);
  }
}

/** Shared raw-JSON fallback block. Used both for unknown event types
 * and for `task.updated` payloads that don't declare a `field`. */
function renderRawPayload(payload: unknown): ReactNode {
  return (
    <pre
      style={{
        marginBlockStart: 'var(--nf-space-1)',
        padding: 'var(--nf-space-2)',
        background: 'var(--nf-color-surface)',
        borderRadius: '0.25rem',
        fontSize: '0.7rem',
        overflowX: 'auto',
      }}
    >
      {JSON.stringify(payload, null, 2)}
    </pre>
  );
}

/** Normalize a free-form reason string into a slug that can be looked up
 * under `timeline:payload.reasons.<slug>`. Lowercases ASCII letters and
 * collapses any run of non-alphanumeric characters into a single
 * underscore so the em-dash, en-dash, or plain hyphen variants all hash
 * to the same bucket. Reasons outside the catalog fall through to the
 * raw text rendered by the caller. */
function reasonSlug(reason: string): string {
  return reason
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

/** Translate a reason string against the fixed catalog at
 * `timeline:payload.reasons.*`. Returns the original string if no
 * catalog entry matches so free-form reasons still render. */
function translateReason(reason: string, t: TFunction): string {
  const slug = reasonSlug(reason);
  if (slug.length === 0) return reason;
  return t(`payload.reasons.${slug}`, { defaultValue: reason });
}

/** humanizePayload renders a structured summary for payload shapes the
 * timeline knows about, and otherwise falls back to the raw JSON block.
 *
 * Currently covers `task.updated` (and any `task.*_updated` event) which
 * carries `{ field, from?, to?, reason?, auto_action? }`. */
function humanizePayload(type: string, payload: unknown, t: TFunction, locale: string): ReactNode {
  const isTaskUpdated = type.startsWith('task.') && type.endsWith('.updated');
  if (!isTaskUpdated) return renderRawPayload(payload);
  if (!payload || typeof payload !== 'object') return renderRawPayload(payload);

  const p = payload as {
    field?: unknown;
    from?: unknown;
    to?: unknown;
    reason?: unknown;
    // biome-ignore lint/style/useNamingConvention: event payload key from backend
    auto_action?: unknown;
  };
  if (typeof p.field !== 'string' || p.field.length === 0) {
    return renderRawPayload(payload);
  }

  const unassigned = t('payload.unassigned', { defaultValue: '—' });
  const label = fieldLabel(p.field, t);
  const fromStr = formatFieldValue(p.field, p.from, locale, unassigned, t);
  const toStr = formatFieldValue(p.field, p.to, locale, unassigned, t);
  const reason = typeof p.reason === 'string' && p.reason.length > 0 ? p.reason : null;
  const autoAction =
    typeof p.auto_action === 'string' && p.auto_action.length > 0 ? p.auto_action : null;
  const autoActionLabel = t('payload.auto_action_label', { defaultValue: 'auto action' });
  // Reason strings flow through translateReason which consults the fixed
  // catalog at `timeline:payload.reasons.*` and falls back to the raw
  // text for unknown entries. Auto actions map through
  // `timeline:payload.auto_action.*` with the same defaultValue fallback
  // so new enum values degrade to the raw key rather than breaking.
  const reasonTranslated = reason !== null ? translateReason(reason, t) : null;
  const autoActionTranslated =
    autoAction !== null
      ? t(`payload.auto_action.${autoAction}`, { defaultValue: autoAction })
      : null;

  return (
    <div
      style={{
        marginBlockStart: 'var(--nf-space-1)',
        padding: 'var(--nf-space-2)',
        background: 'var(--nf-color-surface)',
        borderRadius: '0.25rem',
        fontSize: 'var(--nf-text-xs)',
        display: 'flex',
        flexDirection: 'column',
        gap: '0.125rem',
      }}
    >
      <div style={{ color: 'var(--nf-color-fg)' }}>
        {label}: {fromStr} → {toStr}
      </div>
      {reasonTranslated !== null ? (
        <div style={{ color: 'var(--nf-color-fg-muted)' }}>{reasonTranslated}</div>
      ) : null}
      {autoActionTranslated !== null ? (
        <div style={{ color: 'var(--nf-color-fg-muted)', fontStyle: 'italic' }}>
          {autoActionLabel}: {autoActionTranslated}
        </div>
      ) : null}
    </div>
  );
}

export default function EventCard({ event }: EventCardProps): ReactElement {
  const { t, i18n } = useTranslation('timeline');
  const locale = i18n.resolvedLanguage ?? 'en';

  const isAgent = Boolean(event.actorAgentId && event.actorAgentId.length > 0);
  const rawAgentName = event.actorAgentName?.trim();
  const rawUserName = event.actorDisplayName?.trim();
  const hasAgentName = isAgent && rawAgentName !== undefined && rawAgentName.length > 0;
  const hasUserName = !isAgent && rawUserName !== undefined && rawUserName.length > 0;
  const displayName = hasAgentName ? rawAgentName : hasUserName ? rawUserName : undefined;
  const hasName = displayName !== undefined;
  const agentName = hasName ? displayName : t('actor.system');
  const actorLabel = isAgent ? t('actor.actor_agent', { name: agentName }) : agentName;
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

  // Actor avatar URL: only resolve when the event carries a real user
  // id. Agent / MCP / system events have no user behind them, so they
  // fall through to the initials placeholder rendered by the Avatar
  // primitive — agent events get a bot glyph in place of the avatar.
  const actorAvatarSrc =
    !isAgent && event.actorUserId && event.actorUserId.length > 0
      ? buildAvatarUrl(event.actorUserId, authApiBaseUrl)
      : undefined;

  return (
    <div
      style={{
        position: 'relative',
        display: 'grid',
        gridTemplateColumns: 'auto 1fr',
        columnGap: '0.875rem',
        paddingInlineStart: 'var(--nf-space-1)',
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
          {isAgent ? (
            <span
              role="img"
              aria-label={actorLabel}
              style={{
                inlineSize: '1.25rem',
                blockSize: '1.25rem',
                borderRadius: '999px',
                background: 'color-mix(in oklab, var(--nf-color-accent) 14%, transparent)',
                color: 'var(--nf-color-accent)',
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <Bot size={12} strokeWidth={1.75} aria-hidden="true" />
            </span>
          ) : (
            <Avatar
              alt={actorLabel}
              initials={initials}
              size="sm"
              {...(actorAvatarSrc ? { src: actorAvatarSrc } : {})}
            />
          )}
        </div>
      </div>

      <div
        style={{
          gridColumn: '2 / 3',
          minInlineSize: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1)',
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'baseline',
            gap: 'var(--nf-space-2)',
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
              fontSize: 'var(--nf-text-xs)',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {formatRelative(event.occurredAt, locale)}
          </span>
        </div>
        {payloadVisible ? (
          <details>
            <summary
              style={{
                cursor: 'pointer',
                color: 'var(--nf-color-fg-muted)',
                fontSize: 'var(--nf-text-xs)',
              }}
            >
              {kindLabel}
            </summary>
            {humanizePayload(event.type, event.payload, t, locale)}
          </details>
        ) : null}
      </div>
    </div>
  );
}

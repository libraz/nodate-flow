/**
 * ActivityRow — single unified-activity entry rendered as a list item with
 * an actor avatar/kind glyph, a localized action label, the resource type +
 * id, a source chip, and a relative timestamp. A severity accent (token
 * color) lines the inline-start edge.
 *
 * Mirrors the audit-log row's column shape and the timeline EventCard's
 * actor/avatar treatment so the feed feels native to both surfaces.
 */

import { buildAvatarUrl } from '@nodate-flow/sdk';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import type { TFunction } from 'i18next';
import { Bot, Cog } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { authApiBaseUrl } from '../../lib/sdk';

import type { ActivityEntry } from './api';
import {
  actionLabel,
  actorKindLabel,
  severityAccentVar,
  severityLabel,
  sourceAccentVar,
  sourceLabel,
} from './labels';
import { formatAbsolute, formatRelative } from './relative-time';

export interface ActivityRowProps {
  entry: ActivityEntry;
  /** Resolved display name for `actorUserPublicId`, when known. */
  actorName?: string | undefined;
}

/** Up-to-2-letter initials for an avatar, never derived from a raw UUID. */
function initialsOf(name: string): string {
  const parts = name
    .split(/\s+/)
    .filter((w) => w.length > 0)
    .slice(0, 2)
    .map((w) => (w[0] ?? '').toUpperCase())
    .join('');
  return parts.length > 0 ? parts : (name[0] ?? '').toUpperCase();
}

function ActorGlyph({
  entry,
  actorName,
  actorLabel,
}: {
  entry: ActivityEntry;
  actorName: string | undefined;
  actorLabel: string;
}): ReactElement {
  if (entry.actorKind === 'agent') {
    return (
      <span
        aria-label={actorLabel}
        style={{
          inlineSize: '1.75rem',
          blockSize: '1.75rem',
          borderRadius: '999px',
          background: 'color-mix(in oklab, var(--nf-color-accent) 14%, transparent)',
          color: 'var(--nf-color-accent)',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Bot size={14} strokeWidth={1.75} aria-hidden="true" />
      </span>
    );
  }
  if (entry.actorKind === 'system' || !entry.actorUserPublicId) {
    return (
      <span
        aria-label={actorLabel}
        style={{
          inlineSize: '1.75rem',
          blockSize: '1.75rem',
          borderRadius: '999px',
          background: 'var(--nf-color-bg-sunken)',
          color: 'var(--nf-color-fg-muted)',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Cog size={14} strokeWidth={1.75} aria-hidden="true" />
      </span>
    );
  }
  const name = actorName ?? '';
  const initials = name.length > 0 ? initialsOf(name) : '?';
  const src = buildAvatarUrl(entry.actorUserPublicId, authApiBaseUrl);
  return <Avatar alt={actorLabel} initials={initials} size="sm" src={src} />;
}

function resolveActorLabel(
  entry: ActivityEntry,
  actorName: string | undefined,
  t: TFunction,
): string {
  if (entry.actorKind === 'agent') return t('actor.agent');
  if (entry.actorKind === 'system') return t('actor.system');
  if (actorName && actorName.length > 0) return actorName;
  return t('actor.unknown');
}

export default function ActivityRow({ entry, actorName }: ActivityRowProps): ReactElement {
  const { t, i18n } = useTranslation('activity');
  const locale = i18n.resolvedLanguage ?? 'en';

  const actorLabel = resolveActorLabel(entry, actorName, t);
  const action = actionLabel(entry.action, t);
  const source = sourceLabel(entry.source, t);
  const kind = actorKindLabel(entry.actorKind, t);
  const severity = severityLabel(entry.severity, t);
  const accent = severityAccentVar(entry.severity);
  const sourceTone = sourceAccentVar(entry.source);
  const relative = formatRelative(entry.occurredAt, locale);
  const absolute = formatAbsolute(entry.occurredAt, locale);

  const resourceText =
    entry.resourcePublicId && entry.resourcePublicId.length > 0
      ? t('row.resource_with_id', { type: entry.resourceType, id: entry.resourcePublicId })
      : entry.resourceType;

  return (
    <li
      style={{
        display: 'grid',
        gridTemplateColumns: 'auto 1fr',
        columnGap: '0.875rem',
        alignItems: 'start',
        padding: '0.625rem 0.75rem',
        borderInlineStart: `3px solid ${accent}`,
        borderBlockEnd: '1px solid var(--nf-color-border)',
      }}
    >
      <ActorGlyph entry={entry} actorName={actorName} actorLabel={actorLabel} />

      <div style={{ minInlineSize: 0, display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: '0.5rem', flexWrap: 'wrap' }}>
          <span style={{ color: 'var(--nf-color-fg)', fontWeight: 500 }}>{actorLabel}</span>
          <span style={{ color: 'var(--nf-color-fg)', wordBreak: 'break-word' }}>{action}</span>
          <span
            style={{
              padding: '0 0.375rem',
              borderRadius: '0.25rem',
              border: `1px solid ${sourceTone}`,
              color: sourceTone,
              fontSize: '0.625rem',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}
          >
            {source}
          </span>
          <time
            dateTime={new Date(entry.occurredAt * 1000).toISOString()}
            title={absolute}
            style={{
              marginInlineStart: 'auto',
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-xs)',
              fontVariantNumeric: 'tabular-nums',
              whiteSpace: 'nowrap',
            }}
          >
            {relative}
          </time>
        </div>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '0.5rem',
            flexWrap: 'wrap',
            fontSize: 'var(--nf-text-xs)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              maxInlineSize: '24rem',
              whiteSpace: 'nowrap',
            }}
          >
            {resourceText}
          </span>
          <span aria-hidden="true">·</span>
          <span>{kind}</span>
          <span aria-hidden="true">·</span>
          <span>{severity}</span>
        </div>
      </div>
    </li>
  );
}

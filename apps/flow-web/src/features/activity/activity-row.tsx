/**
 * ActivityRow — single unified-activity entry rendered as a list item with
 * an actor avatar/kind glyph, a localized action label, the resource type +
 * id, a source chip, and a relative timestamp. Severity shows as a token
 * coloured dot beside its label.
 *
 * Mirrors the audit-log row's column shape and the timeline EventCard's
 * actor/avatar treatment so the feed feels native to both surfaces.
 */

import { buildAvatarUrl } from '@nodate-flow/sdk';
import { cx } from '@nodate-flow/ui/lib/cx';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import type { TFunction } from 'i18next';
import { Bot, Cog } from 'lucide-react';
import type { CSSProperties, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { authApiBaseUrl } from '../../lib/sdk';

import styles from './activity.module.css';
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
  /** First-load stagger style from the list (`--stagger-index`). */
  enterStyle?: CSSProperties | undefined;
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
      <span role="img" aria-label={actorLabel} className={cx(styles.glyph, styles.glyphAgent)}>
        <Bot size={14} strokeWidth={1.75} aria-hidden="true" />
      </span>
    );
  }
  if (entry.actorKind === 'system' || !entry.actorUserPublicId) {
    return (
      <span role="img" aria-label={actorLabel} className={styles.glyph}>
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

export default function ActivityRow({
  entry,
  actorName,
  enterStyle,
}: ActivityRowProps): ReactElement {
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
      className={styles.row}
      style={
        {
          ...enterStyle,
          '--source-tone': sourceTone,
          '--severity-tone': accent,
        } as CSSProperties
      }
    >
      <ActorGlyph entry={entry} actorName={actorName} actorLabel={actorLabel} />

      <div className={styles.body}>
        <div className={styles.headline}>
          <span className={styles.actor}>{actorLabel}</span>
          <span className={styles.action}>{action}</span>
          <span className={styles.sourceChip}>{source}</span>
          <time
            dateTime={new Date(entry.occurredAt * 1000).toISOString()}
            title={absolute}
            className={styles.time}
          >
            {relative}
          </time>
        </div>
        <div className={styles.meta}>
          <span className={styles.resource}>{resourceText}</span>
          <span aria-hidden="true">·</span>
          <span>{kind}</span>
          <span aria-hidden="true">·</span>
          <span className={styles.severity}>
            <span className={styles.severityDot} aria-hidden="true" />
            {severity}
          </span>
        </div>
      </div>
    </li>
  );
}

/**
 * CreatorChip — subtle "Created by {avatar} {name}" metadata affordance
 * shared by the event dialog header and the event detail page.
 *
 * Reuses the {@link Avatar} primitive and the same initials-fallback
 * convention as {@link import('./attendees-section')} so the creator's
 * identity renders identically to the attendee rows (size `sm`, image or
 * two-letter initials fallback, accessible label carrying the name).
 *
 * Creator is non-sensitive metadata (distinct from owner / email): the
 * API exposes `creatorId` / `creatorDisplayName` / `creatorAvatarUrl?` on
 * the authenticated event DTOs but deliberately omits it from the public
 * share / embed DTOs, so this component is never reached from those views.
 *
 * The creating user may have been disabled, in which case the API returns
 * no creator fields; this component renders nothing (`null`) rather than a
 * broken placeholder.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './creator-chip.module.css';

/** Two-letter initials fallback for avatars without a picture. */
function initialsOf(displayName: string): string {
  const trimmed = displayName.trim();
  if (!trimmed) return '?';
  const parts = trimmed.split(/\s+/u);
  const first = parts[0]?.[0] ?? '';
  const second = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + second).toUpperCase();
}

export interface CreatorChipProps {
  /** Creator display name, or null/undefined when the creator is unknown. */
  displayName: string | null | undefined;
  /** Optional avatar image URL. */
  avatarUrl?: string | undefined;
}

/**
 * CreatorChip — renders a localized "Created by" label with the creator's
 * avatar and display name. Returns `null` when no creator is available.
 */
export default function CreatorChip({
  displayName,
  avatarUrl,
}: CreatorChipProps): ReactElement | null {
  const { t } = useTranslation('calendar-events');

  const name = displayName?.trim();
  if (!name) return null;

  const label = t('event.creator.createdBy', { name });

  return (
    <div className={styles.root} role="group" aria-label={label}>
      <span className={styles.prefix}>{t('event.creator.label')}</span>
      <Avatar
        size="sm"
        alt={name}
        initials={initialsOf(name)}
        {...(avatarUrl ? { src: avatarUrl } : {})}
      />
      <span className={styles.name}>{name}</span>
    </div>
  );
}

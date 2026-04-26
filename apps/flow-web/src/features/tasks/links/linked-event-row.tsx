/**
 * LinkedEventRow — single linked-event row in the section list.
 *
 * Renders a four-cell grid: relation glyph, event title (anchor), the
 * formatted time (`<LinkedEventTime>`), and a discreet unlink button
 * that fades in on row hover / focus. The glyph cell shows a small
 * accent dot to the inline-start when the event lies on the local
 * "today" so the eye can scan for what is happening now.
 *
 * Removal is animated: clicking the unlink button flips
 * `data-removing="true"` on the row to drive the `nf-link-shake-out`
 * keyframes in the section CSS, then calls `onUnlink` ~200ms later so
 * the optimistic removal lines up with the animation. The row never
 * waits on the network — `useUnlinkEvent` removes the link from the
 * cache before the request fires.
 *
 * Optimistic rows (those whose id starts with `optimistic-`) have the
 * unlink control disabled because the server-issued id is not yet
 * available; once the mutation settles the row gets a real id and the
 * control re-enables.
 *
 * AI-driven relations (`depends_on` / `prep_for`) fall back to the
 * `contributes_to` glyph so manual UI doesn't introduce a new visual
 * vocabulary for kinds the picker can't even create.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import LinkedEventGlyph from './linked-event-glyph';
import LinkedEventTime, { isToday } from './linked-event-time';
import styles from './linked-events.module.css';
import type { LinkKind, LinkRelation, TaskEventLink } from './types';

const REMOVE_ANIMATION_MS = 200;

export interface LinkedEventRowProps {
  link: TaskEventLink;
  locale: string;
  onUnlink: (linkId: string) => void;
  isOptimistic: boolean;
}

/**
 * Narrow the SDK's open-ended `relation: string` to a glyph kind. AI
 * relations fall back to `contributes_to` because the manual UI only
 * has glyphs for the two kinds the picker creates.
 */
function relationToKind(relation: string): LinkKind {
  if (relation === 'blocks') return 'blocks';
  // contributes_to / depends_on / prep_for / unknown -> contributes
  return 'contributes_to';
}

export default function LinkedEventRow({
  link,
  locale,
  onUnlink,
  isOptimistic,
}: LinkedEventRowProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  const [isRemoving, setIsRemoving] = useState(false);
  const timerRef = useRef<number | null>(null);

  // External-clock cleanup is the canonical Effects-as-sync surface:
  // we own a window timer and have to cancel it on unmount.
  useEffect(
    () => () => {
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
      }
    },
    [],
  );

  const kind = relationToKind(link.relation as LinkRelation);
  const title = link.eventTitle ?? '';
  const today = isToday(link.eventStartAt);

  const handleUnlinkClick = (): void => {
    if (isOptimistic) return;
    setIsRemoving(true);
    timerRef.current = window.setTimeout(() => {
      onUnlink(link.id);
    }, REMOVE_ANIMATION_MS);
  };

  return (
    <li className={styles.row} data-removing={isRemoving ? 'true' : undefined}>
      <span className={styles.glyphCell} data-today={today ? 'true' : undefined}>
        <LinkedEventGlyph kind={kind} />
        {/* visually-hidden description so screen readers hear the relation kind */}
        <span
          style={{
            position: 'absolute',
            inlineSize: '1px',
            blockSize: '1px',
            padding: 0,
            overflow: 'hidden',
            clip: 'rect(0, 0, 0, 0)',
            whiteSpace: 'nowrap',
            borderWidth: 0,
          }}
        >
          {kind === 'blocks' ? t('kind.blocks') : t('kind.contributesTo')}
        </span>
      </span>
      {/* biome-ignore lint/a11y/useValidAnchor: placeholder until /calendar?event= deep-link lands; the row navigates to the calendar event detail page in a follow-up */}
      <a
        href="#"
        className={styles.rowTitle}
        aria-label={t('row.openEvent', { title })}
        onClick={(e) => {
          // Placeholder href; the full /calendar?event= deep-link is a
          // follow-up. Prevent the default jump to top so the row stays
          // visible on click.
          e.preventDefault();
        }}
      >
        {title}
      </a>
      <LinkedEventTime
        epochStartSec={link.eventStartAt}
        allDay={link.eventAllDay ?? false}
        locale={locale}
      />
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className={styles.unlinkBtn}
        title={t('row.unlink')}
        aria-label={t('row.unlinkAria', { title })}
        disabled={isOptimistic}
        onClick={handleUnlinkClick}
      >
        ×
      </Button>
    </li>
  );
}

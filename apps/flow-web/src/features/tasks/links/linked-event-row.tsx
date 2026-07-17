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
import { Link } from '@tanstack/react-router';
import { type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useActiveWorkspaceId } from '../../../lib/use-current-workspace';
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
  const workspaceId = useActiveWorkspaceId();
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

  // Deep-link target for the calendar event detail route. Linked events
  // are always searched within the task's own workspace, so the active
  // workspace id (resolved from the `/tasks/$taskId` route) owns the
  // calendar the event lives on. Null when any coordinate is missing, in
  // which case the title renders as plain, non-interactive text.
  const eventTarget =
    workspaceId && link.calendarId && link.eventId
      ? { id: workspaceId, calId: link.calendarId, evtId: link.eventId }
      : null;

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
      {eventTarget ? (
        <Link
          to="/workspaces/$id/calendars/$calId/events/$evtId"
          params={eventTarget}
          className={styles.rowTitle}
          aria-label={t('row.openEvent', { title })}
        >
          {title}
        </Link>
      ) : (
        // Missing workspace context or event coordinates: render the
        // title as plain text rather than advertise navigation the row
        // cannot perform.
        <span className={styles.rowTitle}>{title}</span>
      )}
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

/**
 * SnoozePopover — three quick-snooze buttons (+1h / tomorrow / next week).
 * All target timestamps are computed client-side as unix seconds and passed
 * back through `onSnooze`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import { Day, type Zone } from '@nodate-flow/ui/time';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { useEffectiveZone } from '../../lib/use-effective-timezone';

/** The hour "morning" means for the two day-shaped snooze targets. */
const MORNING_HOUR = 9;

export interface SnoozePopoverProps {
  /** Trigger element rendered outside the panel. */
  children: ReactElement;
  /** Called with the target unix-seconds timestamp. */
  onSnooze: (snoozeUntil: number) => void;
}

/**
 * A relative offset is zone-free: an hour from now is an hour from now
 * wherever the reader is.
 */
function plusHours(hours: number): number {
  return Math.floor((Date.now() + hours * 3600 * 1000) / 1000);
}

/**
 * 09:00 on the day `dayOffset` days from today, read in `zone`.
 *
 * "Tomorrow morning" is a wall clock on a calendar day, so both halves
 * are zone questions and neither is the browser's. Decided in the
 * browser's zone, an item snoozed by someone travelling reappeared at
 * their host's 9am rather than their own — and on a day that was not
 * always tomorrow. Stepping days on a {@link Day} rather than adding
 * 86,400 seconds also keeps 9am at 9am across a DST transition.
 */
export function morningIn(zone: Zone, dayOffset: number): number {
  return Math.floor(Day.today(zone).addDays(dayOffset).at(zone, MORNING_HOUR).toSeconds());
}

export default function SnoozePopover({ children, onSnooze }: SnoozePopoverProps): ReactElement {
  const { t } = useTranslation('inbox');
  const zone = useEffectiveZone();

  const content: ReactNode = (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-1)',
        padding: 'var(--nf-space-1-5)',
        // nf-token-override: component dimension, not a spacing step
        minInlineSize: '10rem',
      }}
    >
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => {
          onSnooze(plusHours(1));
        }}
      >
        {t('snooze.one_hour')}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => {
          onSnooze(morningIn(zone, 1));
        }}
      >
        {t('snooze.tomorrow')}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => {
          onSnooze(morningIn(zone, 7));
        }}
      >
        {t('snooze.next_week')}
      </Button>
    </div>
  );

  return <Popover content={content}>{children}</Popover>;
}

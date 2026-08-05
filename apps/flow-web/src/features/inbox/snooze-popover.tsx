/**
 * SnoozePopover — three quick-snooze buttons (+1h / tomorrow / next week).
 * All target timestamps are computed client-side as unix seconds and passed
 * back through `onSnooze`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export interface SnoozePopoverProps {
  /** Trigger element rendered outside the panel. */
  children: ReactElement;
  /** Called with the target unix-seconds timestamp. */
  onSnooze: (snoozeUntil: number) => void;
}

function plusHours(hours: number): number {
  return Math.floor((Date.now() + hours * 3600 * 1000) / 1000);
}

function tomorrowMorning(): number {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  d.setHours(9, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

function nextWeekMorning(): number {
  const d = new Date();
  d.setDate(d.getDate() + 7);
  d.setHours(9, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

export default function SnoozePopover({ children, onSnooze }: SnoozePopoverProps): ReactElement {
  const { t } = useTranslation('inbox');

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
          onSnooze(tomorrowMorning());
        }}
      >
        {t('snooze.tomorrow')}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => {
          onSnooze(nextWeekMorning());
        }}
      >
        {t('snooze.next_week')}
      </Button>
    </div>
  );

  return <Popover content={content}>{children}</Popover>;
}

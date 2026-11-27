/**
 * TaskSideCard / TaskSideGroup / TaskSideCardHeading — the sidebar card shell
 * of the task detail page, its hairline-divided groups, and the heading shared
 * by every card and sub-section in it.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Card, { type CardProps } from '@nodate-flow/ui/primitives/card';
import Popover from '@nodate-flow/ui/primitives/popover';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { CircleHelp } from 'lucide-react';
import { type HTMLAttributes, type ReactElement, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import css from './task-side-card.module.css';

export function TaskSideCard({ className, ...rest }: CardProps): ReactElement {
  return <Card className={cx(css.card, className)} {...rest} />;
}

/** A block inside a card; consecutive groups are divided by one hairline. */
export function TaskSideGroup({
  className,
  ...rest
}: HTMLAttributes<HTMLDivElement>): ReactElement {
  return <div className={cx(css.group, className)} {...rest} />;
}

export interface HintTipProps {
  /** Name of the thing the hint explains; goes into the button's accessible name. */
  title: string;
  hint: string;
}

const NO_HOVER_QUERY = '(hover: none)';

function subscribeNoHover(notify: () => void): () => void {
  const mql = window.matchMedia(NO_HOVER_QUERY);
  mql.addEventListener('change', notify);
  return () => mql.removeEventListener('change', notify);
}

function readNoHover(): boolean {
  return window.matchMedia(NO_HOVER_QUERY).matches;
}

/** Touch has no hover, and the Tooltip primitive does not open on tap: there the hint is a popover. */
function useNoHover(): boolean {
  return useSyncExternalStore(subscribeNoHover, readNoHover, () => false);
}

/**
 * Small muted info button directly after a heading. On hover and keyboard
 * focus it shows `hint` in a tooltip; where there is no hover it opens the
 * same text in a popover on tap.
 */
export function HintTip({ title, hint }: HintTipProps): ReactElement {
  const { t } = useTranslation('common');
  const noHover = useNoHover();
  const button = (
    <button
      type="button"
      className={css.hintButton}
      aria-label={t('tasks.detail.hint_label', { title })}
    >
      <CircleHelp size={14} aria-hidden />
    </button>
  );
  if (noHover) {
    return (
      <Popover
        content={<p className={css.hintText}>{hint}</p>}
        ariaLabel={t('tasks.detail.hint_label', { title })}
      >
        {button}
      </Popover>
    );
  }
  return (
    <Tooltip content={hint} className={css.hintText}>
      {button}
    </Tooltip>
  );
}

export interface TaskSideCardHeadingProps {
  title: string;
  /** Explanation, shown in a tooltip behind an info button next to the title. */
  hint?: string;
  /** Element at the end of the row (a badge, an action). */
  marker?: ReactElement;
  /** `card` is an h2 for a whole card; `sub` is an h3 for a section inside one. */
  level?: 'card' | 'sub';
}

export function TaskSideCardHeading({
  title,
  hint,
  marker,
  level = 'card',
}: TaskSideCardHeadingProps): ReactElement {
  const Heading = level === 'sub' ? 'h3' : 'h2';
  return (
    <div className={css.titleRow}>
      <div className={css.titleGroup}>
        <Heading className={cx(css.title, level === 'sub' && css.subTitle)}>{title}</Heading>
        {hint ? <HintTip title={title} hint={hint} /> : null}
      </div>
      {marker}
    </div>
  );
}

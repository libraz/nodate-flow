/**
 * TaskActionButton — the tertiary action of the task detail page: a ghost
 * button with a leading icon and a text label, in the accent foreground so it
 * reads as actionable. Section, property and block-level actions all use it;
 * the primary (recommended transition) and secondary (commit) tiers stay on
 * `Button` directly.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Button, { type ButtonProps } from '@nodate-flow/ui/primitives/button';
import type { ReactElement, ReactNode, Ref } from 'react';
import css from './task-action-button.module.css';

export interface TaskActionButtonProps extends Omit<ButtonProps, 'variant' | 'size' | 'as'> {
  /** Leading icon, always present: the label alone does not read as a control. */
  icon: ReactNode;
  ref?: Ref<HTMLButtonElement>;
}

export default function TaskActionButton({
  icon,
  className,
  children,
  ...rest
}: TaskActionButtonProps): ReactElement {
  return (
    <Button variant="ghost" size="sm" className={cx(css.action, className)} {...rest}>
      {icon}
      {children}
    </Button>
  );
}

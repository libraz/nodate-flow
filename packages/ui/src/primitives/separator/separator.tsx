/**
 * Separator — thin hairline divider. Horizontal by default.
 * Decorative separators are marked `aria-hidden` and given `role="none"`.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './separator.module.css';

export type SeparatorOrientation = 'horizontal' | 'vertical';

export interface SeparatorProps extends HTMLAttributes<HTMLDivElement> {
  /** Visual orientation. Defaults to `"horizontal"`. */
  orientation?: SeparatorOrientation;
  /** When true (default), separator is purely decorative. */
  decorative?: boolean;
}

function SeparatorImpl(
  {
    className,
    orientation = 'horizontal',
    decorative = true,
    role,
    'aria-orientation': ariaOrientation,
    ...rest
  }: SeparatorProps,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  return (
    <div
      ref={ref}
      role={role ?? (decorative ? 'none' : 'separator')}
      aria-orientation={decorative ? undefined : (ariaOrientation ?? orientation)}
      className={cx(
        styles.root,
        orientation === 'vertical' ? styles.vertical : styles.horizontal,
        className,
      )}
      {...rest}
    />
  );
}

const Separator = forwardRef<HTMLDivElement, SeparatorProps>(SeparatorImpl);
Separator.displayName = 'Separator';

export default Separator;

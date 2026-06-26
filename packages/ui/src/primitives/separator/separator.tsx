/**
 * Separator — thin hairline divider. Horizontal by default.
 * Decorative separators are marked `aria-hidden` and given `role="none"`.
 */

import { forwardRef, type HTMLAttributes, type ReactElement } from 'react';
import { cx } from '../../lib/cx';
import styles from './separator.module.css';

export type SeparatorOrientation = 'horizontal' | 'vertical';

export interface SeparatorProps extends HTMLAttributes<HTMLDivElement> {
  /** Visual orientation. Defaults to `"horizontal"`. */
  orientation?: SeparatorOrientation;
  /** When true (default), separator is purely decorative. */
  decorative?: boolean;
}

/** Separator renders a thin hairline divider. */
const Separator = forwardRef<HTMLDivElement, SeparatorProps>(
  (
    {
      className,
      orientation = 'horizontal',
      decorative = true,
      role,
      'aria-orientation': ariaOrientation,
      ...rest
    },
    ref,
  ): ReactElement => (
    // biome-ignore lint/a11y/useAriaPropsSupportedByRole: aria-orientation is only emitted in the non-decorative branch, where the role resolves to "separator" which supports it; the conditional is not statically narrowable
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
  ),
);
Separator.displayName = 'Separator';

export default Separator;

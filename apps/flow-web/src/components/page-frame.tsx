/**
 * Outer frame of an authenticated top-level page: page padding, the
 * header/body rhythm and the inline measure. `standard` centres content
 * at `--nf-measure-page`; `full` spans the main panel for grid-like
 * surfaces (calendar, task boards, split editors).
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { ComponentPropsWithoutRef, ReactElement } from 'react';

import styles from './page-frame.module.css';

export type PageFrameWidth = 'standard' | 'full';

export interface PageFrameProps extends ComponentPropsWithoutRef<'div'> {
  width?: PageFrameWidth;
}

export default function PageFrame({
  width = 'standard',
  className,
  children,
  ...rest
}: PageFrameProps): ReactElement {
  return (
    <div
      {...rest}
      data-width={width}
      className={cx(styles.frame, width === 'standard' && styles.standard, className)}
    >
      {children}
    </div>
  );
}

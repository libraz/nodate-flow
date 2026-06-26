/**
 * Skeleton — loading placeholder block. Decorative by default; callers can
 * wrap with an `aria-busy` region to convey loading state to AT.
 */

import { forwardRef, type HTMLAttributes, type ReactElement } from 'react';
import { cx } from '../../lib/cx';
import styles from './skeleton.module.css';

export type SkeletonProps = HTMLAttributes<HTMLDivElement>;

/** Skeleton renders a loading placeholder block. */
const Skeleton = forwardRef<HTMLDivElement, SkeletonProps>(
  ({ className, ...rest }, ref): ReactElement => (
    <div ref={ref} aria-hidden="true" className={cx(styles.root, className)} {...rest} />
  ),
);
Skeleton.displayName = 'Skeleton';

export default Skeleton;

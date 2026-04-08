/**
 * Skeleton — loading placeholder block. Decorative by default; callers can
 * wrap with an `aria-busy` region to convey loading state to AT.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './skeleton.module.css';

export type SkeletonProps = HTMLAttributes<HTMLDivElement>;

function SkeletonImpl(
  { className, ...rest }: SkeletonProps,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  return <div ref={ref} aria-hidden="true" className={cx(styles.root, className)} {...rest} />;
}

const Skeleton = forwardRef<HTMLDivElement, SkeletonProps>(SkeletonImpl);
Skeleton.displayName = 'Skeleton';

export default Skeleton;

/**
 * ScrollArea — themed scroll container with overlay-style scrollbars.
 *
 * Intentionally simple: a styled wrapper around native scrolling. No
 * virtualization, no custom thumbs — we lean on `::-webkit-scrollbar` and
 * `scrollbar-*` properties tied to semantic tokens. Pass any block content
 * as children.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './scroll-area.module.css';

export interface ScrollAreaProps extends HTMLAttributes<HTMLDivElement> {
  /** Maximum block-size for the scroll viewport. */
  maxBlockSize?: string | number;
}

function ScrollAreaImpl(
  { className, style, maxBlockSize, children, ...rest }: ScrollAreaProps,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  const mergedStyle =
    maxBlockSize !== undefined
      ? {
          ...style,
          maxBlockSize: typeof maxBlockSize === 'number' ? `${maxBlockSize}px` : maxBlockSize,
        }
      : style;
  return (
    <div
      ref={ref}
      className={cx(styles.root, className)}
      style={mergedStyle}
      // biome-ignore lint/a11y/noNoninteractiveTabindex: keyboard-scrollable region needs focus
      tabIndex={0}
      role="region"
      {...rest}
    >
      {children}
    </div>
  );
}

const ScrollArea = forwardRef<HTMLDivElement, ScrollAreaProps>(ScrollAreaImpl);
ScrollArea.displayName = 'ScrollArea';

export default ScrollArea;

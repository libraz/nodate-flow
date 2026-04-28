/**
 * Badge — small inline tag with semantic tone variants.
 *
 * Renders a `<span>` by default; pass `as` to render a different element
 * (e.g. `as="a"` for a clickable badge) without losing tone styling.
 */

import {
  type ComponentPropsWithoutRef,
  type ElementType,
  type HTMLAttributes,
  type ReactElement,
  type Ref,
  createElement,
  forwardRef,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './badge.module.css';

export type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

interface BadgeOwnProps {
  /** Semantic tone. Defaults to `"neutral"`. */
  tone?: BadgeTone;
}

export interface BadgeProps
  extends BadgeOwnProps,
    Omit<HTMLAttributes<HTMLSpanElement>, keyof BadgeOwnProps | 'as'> {
  as?: 'span';
}

export type BadgeAsProps<E extends ElementType> = BadgeOwnProps & { as: E } & Omit<
    ComponentPropsWithoutRef<E>,
    keyof BadgeOwnProps | 'as'
  >;

interface BadgeComponent {
  (props: BadgeProps & { ref?: Ref<HTMLSpanElement> }): ReactElement;
  <E extends ElementType>(props: BadgeAsProps<E> & { ref?: Ref<Element> }): ReactElement;
  displayName?: string;
}

const Badge = forwardRef<HTMLElement, BadgeProps & { as?: ElementType }>(
  ({ as, className, tone = 'neutral', ...rest }, ref): ReactElement => {
    const Component: ElementType = as ?? 'span';
    return createElement(Component, {
      ref,
      className: cx(
        styles.root,
        tone === 'success' && styles.success,
        tone === 'warning' && styles.warning,
        tone === 'danger' && styles.danger,
        tone === 'info' && styles.info,
        tone === 'accent' && styles.accent,
        className,
      ),
      ...rest,
    });
  },
) as unknown as BadgeComponent;

Badge.displayName = 'Badge';

export default Badge;

/**
 * Card — primitive container with surface + border + shadow tokens.
 *
 * Renders a `<div>` by default; pass `as` to render a different element
 * (e.g. `as="article"` / `as="section"`) without losing card styling.
 */

import {
  type ComponentPropsWithoutRef,
  createElement,
  type ElementType,
  forwardRef,
  type HTMLAttributes,
  type ReactElement,
  type Ref,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './card.module.css';

interface CardOwnProps {
  /** Use the larger shadow token. */
  elevated?: boolean;
}

export interface CardProps
  extends CardOwnProps,
    Omit<HTMLAttributes<HTMLDivElement>, keyof CardOwnProps | 'as'> {
  as?: 'div';
}

export type CardAsProps<E extends ElementType> = CardOwnProps & { as: E } & Omit<
    ComponentPropsWithoutRef<E>,
    keyof CardOwnProps | 'as'
  >;

interface CardComponent {
  (props: CardProps & { ref?: Ref<HTMLDivElement> }): ReactElement;
  <E extends ElementType>(props: CardAsProps<E> & { ref?: Ref<Element> }): ReactElement;
  displayName?: string;
}

const Card = forwardRef<HTMLElement, CardProps & { as?: ElementType }>(
  ({ as, className, elevated, ...rest }, ref): ReactElement => {
    const Component: ElementType = as ?? 'div';
    return createElement(Component, {
      ref,
      className: cx(styles.root, elevated && styles.elevated, className),
      ...rest,
    });
  },
) as unknown as CardComponent;

Card.displayName = 'Card';

export default Card;

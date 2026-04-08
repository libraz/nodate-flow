/**
 * Card — primitive container with surface + border + shadow tokens.
 */

import { type HTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './card.module.css';

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  /** Use the larger shadow token. */
  elevated?: boolean;
}

function CardImpl(
  { className, elevated, ...rest }: CardProps,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  return (
    <div ref={ref} className={cx(styles.root, elevated && styles.elevated, className)} {...rest} />
  );
}

const Card = forwardRef<HTMLDivElement, CardProps>(CardImpl);
Card.displayName = 'Card';

export default Card;

/**
 * Card — primitive container with surface + border + shadow tokens.
 */

import { type HTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './card.module.css';

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  /** Use the larger shadow token. */
  elevated?: boolean;
}

/** Card renders a surface container with border and shadow. */
const Card = forwardRef<HTMLDivElement, CardProps>(
  ({ className, elevated, ...rest }, ref): ReactElement => (
    <div ref={ref} className={cx(styles.root, elevated && styles.elevated, className)} {...rest} />
  ),
);
Card.displayName = 'Card';

export default Card;

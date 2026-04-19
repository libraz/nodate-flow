/**
 * Button — primitive button with variant and size support.
 *
 * Renders a native `<button>`; all native props (type, onClick, disabled, ...) pass through.
 * Focus is rendered via the design system focus ring token.
 */

import { type ButtonHTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './button.module.css';

export type ButtonVariant = 'default' | 'primary' | 'danger' | 'ghost';
export type ButtonSize = 'sm' | 'md' | 'lg';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Visual variant. Defaults to `"default"`. */
  variant?: ButtonVariant;
  /** Size scale. Defaults to `"md"`. */
  size?: ButtonSize;
}

/** Button renders a styled native `<button>` with variant and size support. */
const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'default', size = 'md', className, type, ...rest }, ref): ReactElement => (
    <button
      ref={ref}
      type={type ?? 'button'}
      className={cx(
        styles.root,
        variant === 'primary' && styles.primary,
        variant === 'danger' && styles.danger,
        variant === 'ghost' && styles.ghost,
        size === 'sm' && styles.sm,
        size === 'lg' && styles.lg,
        className,
      )}
      {...rest}
    />
  ),
);
Button.displayName = 'Button';

export default Button;

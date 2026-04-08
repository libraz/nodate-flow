/**
 * Button — primitive button with variant and size support.
 *
 * Renders a native `<button>`; all native props (type, onClick, disabled, ...) pass through.
 * Focus is rendered via the design system focus ring token.
 */

import { type ButtonHTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
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

function ButtonImpl(
  { variant = 'default', size = 'md', className, type, ...rest }: ButtonProps,
  ref: Ref<HTMLButtonElement>,
): ReactElement {
  return (
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
  );
}

const Button = forwardRef<HTMLButtonElement, ButtonProps>(ButtonImpl);
Button.displayName = 'Button';

export default Button;

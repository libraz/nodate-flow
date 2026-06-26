/**
 * Button — primitive button with variant and size support.
 *
 * Renders a native `<button>` by default; pass `as` to render a different
 * element (e.g. `as="a"` for a link styled like a button). Native props for
 * the rendered element pass through. Focus is rendered via the design system
 * focus ring token.
 */

import {
  type ButtonHTMLAttributes,
  type ComponentPropsWithoutRef,
  createElement,
  type ElementType,
  forwardRef,
  type ReactElement,
  type Ref,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './button.module.css';

export type ButtonVariant = 'default' | 'primary' | 'danger' | 'ghost';
export type ButtonSize = 'sm' | 'md' | 'lg';

interface ButtonOwnProps {
  /** Visual variant. Defaults to `"default"`. */
  variant?: ButtonVariant;
  /** Size scale. Defaults to `"md"`. */
  size?: ButtonSize;
}

/**
 * Native `<button>` props plus the variant / size additions. Default surface
 * — kept as a non-generic interface so consumers writing
 * `<Button onClick={(event) => ...}>` get button-element narrowing for free.
 */
export interface ButtonProps
  extends ButtonOwnProps,
    Omit<ButtonHTMLAttributes<HTMLButtonElement>, keyof ButtonOwnProps | 'as'> {
  as?: 'button';
}

/**
 * Polymorphic variant: the caller passes `as={E}` and the prop surface widens
 * to that element's native attributes. Use sparingly — most call sites want
 * the default `<button>` behaviour.
 */
export type ButtonAsProps<E extends ElementType> = ButtonOwnProps & { as: E } & Omit<
    ComponentPropsWithoutRef<E>,
    keyof ButtonOwnProps | 'as'
  >;

function classFor(variant: ButtonVariant, size: ButtonSize, className: string | undefined): string {
  return cx(
    styles.root,
    variant === 'primary' && styles.primary,
    variant === 'danger' && styles.danger,
    variant === 'ghost' && styles.ghost,
    size === 'sm' && styles.sm,
    size === 'lg' && styles.lg,
    className,
  );
}

interface ButtonComponent {
  (props: ButtonProps & { ref?: Ref<HTMLButtonElement> }): ReactElement;
  <E extends ElementType>(props: ButtonAsProps<E> & { ref?: Ref<Element> }): ReactElement;
  displayName?: string;
}

const Button = forwardRef<HTMLElement, ButtonProps & { as?: ElementType }>(
  ({ as, variant = 'default', size = 'md', className, type, ...rest }, ref): ReactElement => {
    const Component: ElementType = as ?? 'button';
    // Native <button> defaults to type="submit" inside a <form>; we always
    // want a non-submitting button unless the caller opts in. Skip the
    // implicit type when rendering a non-button element (anchors etc. don't
    // accept `type`).
    const buttonType = Component === 'button' ? (type ?? 'button') : type;
    return createElement(Component, {
      ref,
      type: buttonType,
      className: classFor(variant, size, className),
      ...rest,
    });
  },
) as unknown as ButtonComponent;

Button.displayName = 'Button';

export default Button;

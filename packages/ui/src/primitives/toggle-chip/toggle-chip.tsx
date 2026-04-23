/**
 * ToggleChip — pill-shaped toggle button used for layer / filter visibility.
 *
 * Renders `<button type="button" aria-pressed={pressed}>`. Space and Enter
 * activate via the native button. Use {@link ToggleChipGroup} to wrap a set
 * of related toggles with a single accessible group label.
 *
 * The optional `color` prop is mapped to a CSS custom property
 * (`--chip-color`). Pass a token such as `var(--nf-cal-task-color)` or a
 * raw CSS color; do not mix tokens with hardcoded values in new call sites.
 */

import {
  type ButtonHTMLAttributes,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
  forwardRef,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './toggle-chip.module.css';

/** Props for {@link ToggleChip}. */
export interface ToggleChipProps
  extends Omit<
    ButtonHTMLAttributes<HTMLButtonElement>,
    'type' | 'aria-pressed' | 'onChange' | 'children'
  > {
  /** Whether the chip is in the pressed ("on") state. */
  pressed: boolean;
  /** Called with the next pressed state on click / keyboard activation. */
  onPressedChange: (pressed: boolean) => void;
  /**
   * CSS color value for the accent dot and the pressed-state fill. Accepts
   * any valid CSS color or `var(--token)` reference. When omitted, the chip
   * falls back to the accent token and no dot is rendered.
   */
  color?: string;
  /**
   * Accessible name. When omitted and `children` is a string, it defaults to
   * that string. Required if `children` is not a plain string.
   */
  label?: string;
  /** Chip content (typically a label). */
  children: ReactNode;
}

/**
 * Extract a plain-text accessible name from a {@link ReactNode} when possible.
 * Only strings and numbers are preserved; everything else returns undefined.
 */
function stringLabel(node: ReactNode): string | undefined {
  if (typeof node === 'string') return node;
  if (typeof node === 'number') return String(node);
  return undefined;
}

/** ToggleChip renders a pill-shaped toggle button with `aria-pressed`. */
const ToggleChip = forwardRef<HTMLButtonElement, ToggleChipProps>(
  (
    {
      pressed,
      onPressedChange,
      color,
      label,
      disabled,
      className,
      onClick,
      children,
      style,
      ...rest
    },
    ref,
  ): ReactElement => {
    const resolvedLabel = label ?? stringLabel(children);
    const mergedStyle: CSSProperties = {
      ...style,
      // CSS custom property consumed by toggle-chip.module.css.
      ...(color != null ? ({ ['--chip-color' as string]: color } as CSSProperties) : {}),
    };

    return (
      <button
        ref={ref}
        type="button"
        aria-pressed={pressed}
        aria-label={resolvedLabel}
        data-pressed={pressed ? 'true' : 'false'}
        disabled={disabled}
        className={cx(styles.root, className)}
        style={mergedStyle}
        onClick={(e) => {
          onClick?.(e);
          if (!e.defaultPrevented) onPressedChange(!pressed);
        }}
        {...rest}
      >
        {color != null ? <span aria-hidden="true" className={styles.dot} /> : null}
        <span>{children}</span>
      </button>
    );
  },
);
ToggleChip.displayName = 'ToggleChip';

/** Props for {@link ToggleChipGroup}. */
export interface ToggleChipGroupProps {
  /** Accessible label announced for the group (`role="group"`, `aria-label`). */
  label: string;
  /** Optional additional class name applied to the group wrapper. */
  className?: string;
  /** Group children — typically one or more {@link ToggleChip} elements. */
  children: ReactNode;
}

/**
 * ToggleChipGroup wraps a set of {@link ToggleChip} elements in a labelled
 * `role="group"` container so assistive tech announces them as a single
 * segmented control (e.g. "Layers: Tasks pressed, Events pressed,
 * Blocks not pressed").
 */
const ToggleChipGroup = forwardRef<HTMLDivElement, ToggleChipGroupProps>(
  ({ label, className, children }, ref): ReactElement => (
    <div ref={ref} role="group" aria-label={label} className={cx(styles.group, className)}>
      {children}
    </div>
  ),
);
ToggleChipGroup.displayName = 'ToggleChipGroup';

export { ToggleChip, ToggleChipGroup };
export default ToggleChip;

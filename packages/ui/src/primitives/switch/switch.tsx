/**
 * Switch — primitive toggle. Renders a `<button role="switch">` and supports
 * both controlled (`checked` + `onCheckedChange`) and uncontrolled
 * (`defaultChecked`) usage. Space / Enter toggle via native button behavior.
 */

import { type ButtonHTMLAttributes, type ReactElement, forwardRef } from 'react';
import { useControllableState } from '../../hooks/use-controllable-state';
import { cx } from '../../lib/cx';
import styles from './switch.module.css';

export interface SwitchProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onChange' | 'type' | 'role'> {
  /** Controlled checked state. */
  checked?: boolean;
  /** Uncontrolled initial checked state. */
  defaultChecked?: boolean;
  /** Called with the next checked state. */
  onCheckedChange?: (next: boolean) => void;
}

/** Switch renders a toggle button with `role="switch"`. */
const Switch = forwardRef<HTMLButtonElement, SwitchProps>(
  (
    { checked, defaultChecked, onCheckedChange, disabled, className, onClick, ...rest },
    ref,
  ): ReactElement => {
    const [value = false, setValue] = useControllableState<boolean>({
      value: checked,
      defaultValue: defaultChecked ?? false,
      onChange: onCheckedChange,
    });

    return (
      <button
        ref={ref}
        type="button"
        role="switch"
        aria-checked={value}
        disabled={disabled}
        className={cx(styles.root, className)}
        onClick={(e) => {
          onClick?.(e);
          if (!e.defaultPrevented) setValue(!value);
        }}
        {...rest}
      >
        <span className={styles.thumb} aria-hidden="true" />
      </button>
    );
  },
);
Switch.displayName = 'Switch';

export default Switch;

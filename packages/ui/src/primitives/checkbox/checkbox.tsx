/**
 * Checkbox — primitive native checkbox styled via design tokens.
 * Supports an `indeterminate` prop that maps onto the DOM property.
 */

import {
  forwardRef,
  type InputHTMLAttributes,
  type ReactElement,
  type Ref,
  useEffect,
  useRef,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './checkbox.module.css';

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> {
  /** Sets the `indeterminate` DOM property on the underlying checkbox. */
  indeterminate?: boolean;
}

function CheckboxImpl(
  { className, indeterminate, ...rest }: CheckboxProps,
  forwardedRef: Ref<HTMLInputElement>,
): ReactElement {
  const localRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (localRef.current) {
      localRef.current.indeterminate = indeterminate ?? false;
    }
  }, [indeterminate]);

  return (
    <input
      type="checkbox"
      ref={(node) => {
        localRef.current = node;
        if (typeof forwardedRef === 'function') forwardedRef(node);
        else if (forwardedRef) forwardedRef.current = node;
      }}
      className={cx(styles.root, className)}
      {...rest}
    />
  );
}

const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(CheckboxImpl);
Checkbox.displayName = 'Checkbox';

export default Checkbox;

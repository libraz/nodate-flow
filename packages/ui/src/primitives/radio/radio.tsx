/**
 * Radio — primitive native radio button styled via design tokens.
 * Group multiple Radios via a shared `name` prop, same as a raw `<input type="radio">`.
 */

import { type InputHTMLAttributes, type ReactElement, type Ref, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './radio.module.css';

export type RadioProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>;

function RadioImpl({ className, ...rest }: RadioProps, ref: Ref<HTMLInputElement>): ReactElement {
  return <input ref={ref} type="radio" className={cx(styles.root, className)} {...rest} />;
}

const Radio = forwardRef<HTMLInputElement, RadioProps>(RadioImpl);
Radio.displayName = 'Radio';

export default Radio;

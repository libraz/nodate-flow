/**
 * Icon — wraps a lucide-react icon component and enforces, at the type level,
 * that the caller either provides an accessible label OR explicitly marks the
 * icon as decorative via `decorative: true`.
 *
 * @example Decorative
 * ```tsx
 * <Icon icon={Check} decorative />
 * ```
 *
 * @example Labelled
 * ```tsx
 * <Icon icon={Trash2} label={t('common.delete')} />
 * ```
 */

import type { LucideIcon, LucideProps } from 'lucide-react';
import type { JSX } from 'react';

interface IconBase extends Omit<LucideProps, 'aria-label' | 'aria-hidden'> {
  icon: LucideIcon;
}

interface IconDecorative extends IconBase {
  decorative: true;
  label?: never;
}

interface IconLabelled extends IconBase {
  decorative?: false;
  /** Already-translated accessible label. Pass `t('...')`, never a raw key. */
  label: string;
}

export type IconProps = IconDecorative | IconLabelled;

export default function Icon(props: IconProps): JSX.Element {
  const { icon: Component, decorative, label, ...rest } = props;
  if (decorative) {
    return <Component aria-hidden focusable={false} {...rest} />;
  }
  return <Component role="img" aria-label={label} focusable={false} {...rest} />;
}

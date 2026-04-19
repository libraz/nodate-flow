/**
 * Avatar — user avatar primitive. Shows an image when `src` is provided,
 * otherwise falls back to `initials` text. Always requires an `alt` for a11y.
 */

import { type HTMLAttributes, type ReactElement, forwardRef } from 'react';
import { cx } from '../../lib/cx';
import styles from './avatar.module.css';

export type AvatarSize = 'sm' | 'md' | 'lg';

export interface AvatarProps extends HTMLAttributes<HTMLSpanElement> {
  /** Image source. When undefined, initials are rendered instead. */
  src?: string;
  /** Accessible label for the avatar (image alt or fallback aria-label). */
  alt: string;
  /** Text fallback when no `src` is provided. */
  initials?: string;
  /** Size scale. Defaults to `"md"`. */
  size?: AvatarSize;
}

/** Avatar displays a user image or initials fallback. */
const Avatar = forwardRef<HTMLSpanElement, AvatarProps>(
  ({ className, src, alt, initials, size = 'md', ...rest }, ref): ReactElement => (
    <span
      ref={ref}
      role="img"
      aria-label={alt}
      className={cx(styles.root, size === 'sm' && styles.sm, size === 'lg' && styles.lg, className)}
      {...rest}
    >
      {src ? <img className={styles.image} src={src} alt="" /> : initials}
    </span>
  ),
);
Avatar.displayName = 'Avatar';

export default Avatar;

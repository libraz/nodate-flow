/**
 * BrandMark -- the product's gradient tile with the Route glyph, shown where
 * no deployment logo is configured. 'lg' is the signed-out hero (with a
 * pulsing halo); 'sm' is the compact tile for sidebars and headers.
 * The halo's negative z-index relies on the caller's stacking context.
 */

import { Route } from 'lucide-react';
import type { ReactElement } from 'react';
import styles from './brand-mark.module.css';

export interface BrandMarkProps {
  size: 'lg' | 'sm';
}

export default function BrandMark({ size }: BrandMarkProps): ReactElement {
  return (
    <div
      aria-hidden
      data-brand-mark={size}
      className={`${styles.mark} ${size === 'lg' ? styles.lg : styles.sm}`}
    >
      {size === 'lg' ? <span className={styles.halo} /> : null}
      <Route aria-hidden className={styles.glyph} />
    </div>
  );
}

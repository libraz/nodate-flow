/**
 * BrandImage -- renders a deployment brand image (logo or icon). The caller's
 * `className` sets the height and max width; with a dark source, two imgs are
 * rendered and CSS shows exactly one per theme, so there is no first-paint flash.
 */

import type { ReactElement } from 'react';
import type { BrandAsset } from '../../brand/brand';
import styles from './brand-image.module.css';

export interface BrandImageProps {
  asset: BrandAsset;
  alt: string;
  className?: string | undefined;
}

export default function BrandImage({ asset, alt, className }: BrandImageProps): ReactElement {
  const root = className ? `${styles.root} ${className}` : styles.root;
  if (asset.darkSrc === undefined) {
    return (
      <span className={root}>
        <img src={asset.src} alt={alt} className={styles.img} />
      </span>
    );
  }
  return (
    <span className={root}>
      <img src={asset.src} alt={alt} className={`${styles.img} ${styles.light}`} />
      <img src={asset.darkSrc} alt={alt} className={`${styles.img} ${styles.dark}`} />
    </span>
  );
}

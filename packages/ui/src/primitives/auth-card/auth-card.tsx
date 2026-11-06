/**
 * AuthCard -- centered card layout for authentication and account management
 * pages. Renders the deployment logo, or the icon (or brand mark) with the
 * wordmark, above a single translucent panel, over a decorative atmosphere layer that
 * themes switch on with `--nf-atmosphere-opacity`.
 */

import type { ReactElement, ReactNode } from 'react';
import type { BrandAsset } from '../../brand/brand';
import BrandImage from '../brand-image/brand-image';
import BrandMark from '../brand-mark/brand-mark';
import styles from './auth-card.module.css';

export interface AuthCardProps {
  children: ReactNode;
  /** Deployment product name shown as the wordmark. */
  appName: string;
  /** Wordmark-bearing logo; replaces the brand mark and the wordmark (alt = appName). */
  logo?: BrandAsset | undefined;
  /** Square icon; replaces the brand mark, shown above the wordmark. */
  icon?: BrandAsset | undefined;
  /**
   * Controls the card width. 'narrow' (default) uses ~26rem and fits auth
   * flows; 'wide' uses ~36rem for settings pages with longer form fields
   * like IANA timezone names.
   */
  width?: 'narrow' | 'wide';
}

export default function AuthCard({
  children,
  appName,
  logo,
  icon,
  width = 'narrow',
}: AuthCardProps): ReactElement {
  return (
    <main className={styles.root}>
      <div aria-hidden className={styles.atmosphere}>
        <span className={`${styles.orb} ${styles.orb1}`} />
        <span className={`${styles.orb} ${styles.orb2}`} />
        <span className={`${styles.orb} ${styles.orb3}`} />
        <span className={styles.grain} />
      </div>
      <div className={styles.brand}>
        {logo ? (
          <BrandImage asset={logo} alt={appName} className={styles.logo} />
        ) : (
          <>
            {icon ? (
              <BrandImage asset={icon} alt="" className={styles.icon} />
            ) : (
              <BrandMark size="lg" />
            )}
            <div aria-hidden className={styles.wordmark}>
              {appName}
            </div>
          </>
        )}
      </div>
      <section className={`${styles.panel} ${width === 'wide' ? styles.wide : styles.narrow}`}>
        {children}
      </section>
    </main>
  );
}

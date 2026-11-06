/**
 * AuthCard -- centered card layout for authentication and account management
 * pages. Renders the brand mark (or the deployment logo) and the wordmark
 * above a single translucent panel, over a decorative atmosphere layer that
 * themes switch on with `--nf-atmosphere-opacity`.
 */

import { Route } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import styles from './auth-card.module.css';

export interface AuthCardProps {
  children: ReactNode;
  /** Deployment product name shown as the wordmark. */
  appName: string;
  /** Optional logo shown in place of the brand mark; decorative (empty alt). */
  logoUrl?: string | undefined;
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
  logoUrl,
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
        {logoUrl ? (
          <img src={logoUrl} alt="" className={styles.logo} />
        ) : (
          <div aria-hidden className={styles.mark}>
            <span className={styles.halo} />
            <Route aria-hidden className={styles.glyph} />
          </div>
        )}
        <div aria-hidden className={styles.wordmark}>
          {appName}
        </div>
      </div>
      <section className={`${styles.panel} ${width === 'wide' ? styles.wide : styles.narrow}`}>
        {children}
      </section>
    </main>
  );
}

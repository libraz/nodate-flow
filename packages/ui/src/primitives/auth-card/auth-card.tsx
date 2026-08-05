/**
 * AuthCard -- centered card layout for authentication and account management
 * pages. Renders the brand wordmark (gradient) above a single bordered panel,
 * keeping the public auth surface visually consistent.
 */

import type { ReactElement, ReactNode } from 'react';

export interface AuthCardProps {
  children: ReactNode;
  /**
   * Controls the card width. 'narrow' (default) uses ~28rem and fits auth
   * flows; 'wide' uses ~36rem for settings pages with longer form fields
   * like IANA timezone names.
   */
  width?: 'narrow' | 'wide';
}

export default function AuthCard({ children, width = 'narrow' }: AuthCardProps): ReactElement {
  const maxWidth = width === 'wide' ? '36rem' : '28rem';
  return (
    <main
      style={{
        minBlockSize: '100dvh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 'var(--nf-space-6)',
        padding: 'var(--nf-space-6, 2rem)',
        background: 'var(--nf-color-bg, var(--color-bg))',
      }}
    >
      <div
        aria-hidden
        style={{
          margin: 0,
          fontFamily: 'var(--nf-font-display)',
          fontWeight: 400,
          // nf-token-override: the product wordmark on the auth surfaces; the only text on the page, sized to the card rather than to a heading level
          fontSize: 'clamp(2rem, 5vw, 3rem)',
          lineHeight: 1,
          letterSpacing: '-0.02em',
          backgroundImage: 'var(--nf-gradient-wordmark)',
          backgroundClip: 'text',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitBackgroundClip: 'text',
          color: 'transparent',
          // biome-ignore lint/style/useNamingConvention: vendor prefix
          WebkitTextFillColor: 'transparent',
        }}
      >
        nodate-flow
      </div>
      <section
        style={{
          inlineSize: `min(${maxWidth}, 100%)`,
          background: 'var(--nf-color-bg-elevated, var(--color-surface))',
          border: 'var(--nf-space-px, 1px) solid var(--nf-color-border, var(--color-hairline))',
          borderRadius: 'var(--nf-radius-lg, 0.75rem)',
          padding: 'var(--nf-space-6, 2rem)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-5, 1.5rem)',
        }}
      >
        {children}
      </section>
    </main>
  );
}

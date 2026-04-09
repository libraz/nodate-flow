/**
 * AuthCard — centered card layout shared by /login and /signup. Renders
 * the brand wordmark (gradient) above a single bordered panel, keeping
 * the public auth surface visually consistent across both pages.
 */

import type { ReactElement, ReactNode } from 'react';

export interface AuthCardProps {
  children: ReactNode;
}

export default function AuthCard({ children }: AuthCardProps): ReactElement {
  return (
    <main
      style={{
        minBlockSize: '100dvh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1.5rem',
        padding: 'var(--nf-space-6, 2rem)',
        background: 'var(--nf-color-bg, var(--color-bg))',
      }}
    >
      <div
        aria-hidden
        style={{
          margin: 0,
          fontFamily: 'var(--font-display)',
          fontWeight: 400,
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
          inlineSize: 'min(28rem, 100%)',
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

/**
 * AuditLogComingSoon — polite empty state for the audit log route.
 *
 * Rendered by the audit-log lazy route's ErrorBoundary fallback while
 * the backend handler `GET /workspaces/{wsId}/audit-logs` is not yet
 * registered. Keeps the route reachable by direct URL so engineers can
 * preview the layout without triggering the global error page.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export default function AuditLogComingSoon(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '0.75rem',
        inlineSize: '100%',
      }}
    >
      <h1
        style={{
          fontFamily: 'var(--font-display)',
          fontSize: 'clamp(1.5rem, 2.5vw, 2rem)',
          margin: 0,
        }}
      >
        {t('audit_log.title')}
      </h1>
      <div
        role="status"
        style={{
          padding: '3rem 1rem',
          textAlign: 'center',
          color: 'var(--nf-color-fg-muted)',
          border: '1px dashed var(--nf-color-border)',
          borderRadius: '0.75rem',
          background: 'var(--nf-color-bg-sunken)',
          fontSize: '0.875rem',
        }}
      >
        <p style={{ margin: 0, fontWeight: 500, color: 'var(--nf-color-fg)' }}>
          {t('audit.empty.title')}
        </p>
        <p style={{ margin: '0.5rem 0 0' }}>{t('audit.empty.body')}</p>
      </div>
    </section>
  );
}

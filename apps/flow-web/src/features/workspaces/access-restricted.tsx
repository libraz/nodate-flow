/**
 * AccessRestricted — shared fallback panel for workspace settings
 * sub-routes that require admin / owner role.
 *
 * Renders a section-level `<h1>` (so the route always contributes a
 * single level-1 heading, matching the settings layout contract) plus a
 * short explanation and a link back to the workspace general settings.
 *
 * Used by the workspace settings ErrorBoundary wrappers to keep the
 * document outline valid even when the underlying data query returns
 * 401 / 403 for a non-admin member.
 */

import { Link, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings');

type SectionTitleKey =
  | 'nav.mcp_tokens'
  | 'nav.auto_actions'
  | 'nav.ai_activity'
  | 'nav.weekly_digest'
  | 'nav.audit_log';

export interface AccessRestrictedProps {
  sectionTitleKey: SectionTitleKey;
}

export default function AccessRestricted({ sectionTitleKey }: AccessRestrictedProps): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();

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
        {t(sectionTitleKey)}
      </h1>
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('access_restricted.body')}</p>
      <p style={{ margin: 0 }}>
        <Link
          to="/workspaces/$id/settings/general"
          params={{ id }}
          style={{ color: 'var(--nf-color-accent)', textDecoration: 'none' }}
        >
          {t('access_restricted.cta')}
        </Link>
      </p>
    </section>
  );
}

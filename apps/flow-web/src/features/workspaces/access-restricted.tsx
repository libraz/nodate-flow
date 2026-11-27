/**
 * AccessRestricted — shared fallback panel for workspace settings
 * sub-routes that require admin / owner role.
 *
 * Renders a section-level `<h1>` (so the route always contributes a
 * single level-1 heading, matching the settings layout contract) plus a
 * short explanation and a link back to the workspace general settings.
 * The `tab` variant sits inside a page that already owns the h1: it renders
 * an `<h2>` and links to the Agents tab, the one every role can open.
 *
 * Used by the workspace settings ErrorBoundary wrappers to keep the
 * document outline valid even when the underlying data query returns
 * 401 / 403 for a non-admin member.
 */

import { getRouteApi, Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../../components/page-header';
import SectionHeader from '../../components/section-header';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings');

type SectionTitleKey =
  | 'nav.mcp_tokens'
  | 'nav.webhooks'
  | 'nav.public_shares'
  | 'nav.ai'
  | 'nav.weekly_digest'
  | 'nav.audit_log';

export interface AccessRestrictedProps {
  sectionTitleKey: SectionTitleKey;
  /** `page` (default) owns the h1; `tab` is a section inside an AI settings tab. */
  variant?: 'page' | 'tab';
}

export default function AccessRestricted({
  sectionTitleKey,
  variant = 'page',
}: AccessRestrictedProps): ReactElement {
  const { t } = useTranslation('settings');
  const { id } = routeApi.useParams();

  if (variant === 'tab') {
    return (
      <section
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
          inlineSize: '100%',
        }}
      >
        <SectionHeader title={t(sectionTitleKey)} description={t('access_restricted.body')} />
        <p style={{ margin: 0 }}>
          <Link
            to="/workspaces/$id/settings/ai/agents"
            params={{ id }}
            style={{ color: 'var(--nf-color-accent-fg)', textDecoration: 'none' }}
          >
            {t('ai_page.tabs.agents')}
          </Link>
        </p>
      </section>
    );
  }

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-6)',
        inlineSize: '100%',
      }}
    >
      <PageHeader title={t(sectionTitleKey)} description={t('access_restricted.body')} />
      <p style={{ margin: 0 }}>
        <Link
          to="/workspaces/$id/settings/general"
          params={{ id }}
          style={{ color: 'var(--nf-color-accent-fg)', textDecoration: 'none' }}
        >
          {t('access_restricted.cta')}
        </Link>
      </p>
    </section>
  );
}

/**
 * AiStatusCard — what AI capabilities work for this workspace right now.
 *
 * Three rows (AI features, task embeddings, auto-actions), each with a
 * state badge plus text and, where the viewer can fix a gap, a link to the
 * tab that fixes it. Non-admins get an "ask an admin" line instead.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Link } from '@tanstack/react-router';
import type { TFunction } from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../../lib/use-current-workspace-role';
import { type AiStatus, useAiStatusQuery } from './ai-status-api';
import styles from './ai-status-card.module.css';

interface StatusRowProps {
  label: string;
  hint: string;
  ok: boolean;
  stateText: string;
  action: ReactNode;
}

function StatusRow({ label, hint, ok, stateText, action }: StatusRowProps): ReactElement {
  return (
    <li className={styles.row}>
      <div className={styles.label}>
        <span className={styles.labelText}>{label}</span>
        <span className={styles.hint}>{hint}</span>
      </div>
      <div className={styles.state}>
        <Badge tone={ok ? 'success' : 'neutral'}>{stateText}</Badge>
      </div>
      <div className={styles.action}>{action}</div>
    </li>
  );
}

function featuresText(provider: AiStatus['provider'], t: TFunction<'settings'>): string {
  const providerName = provider.name ?? '';
  switch (provider.state) {
    case 'ready':
      return provider.model
        ? t('ai_page.status.features.ready', { providerName, model: provider.model })
        : t('ai_page.status.features.ready_no_model', { providerName });
    case 'no_default_model':
      return t('ai_page.status.features.no_default_model', { providerName });
    case 'none':
      return t('ai_page.status.features.none');
    case 'unavailable':
      return t('ai_page.status.features.unavailable');
  }
}

function embeddingText(status: AiStatus['embedding']['status'], t: TFunction<'settings'>): string {
  switch (status) {
    case 'enabled':
      return t('ai_page.status.embedding.enabled');
    case 'disabled':
      return t('ai_page.status.embedding.disabled');
    case 'unconfigured':
      return t('ai_page.status.embedding.unconfigured');
  }
}

export default function AiStatusCard({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const admin = isWorkspaceAdmin(useCurrentWorkspaceRole());
  const { data, isError } = useAiStatusQuery(workspaceId);

  const fixLink = (
    to: '/workspaces/$id/settings/ai/connection' | '/workspaces/$id/settings/ai/automation',
  ) => (
    <Link to={to} params={{ id: workspaceId }} className={styles.link}>
      {t('ai_page.status.fix_link')}
    </Link>
  );
  const askAdmin = <span>{t('ai_page.status.ask_admin')}</span>;

  let body: ReactNode;
  if (isError) {
    body = <p role="alert">{t('ai_page.status.load_failed')}</p>;
  } else if (!data) {
    body = (
      <div aria-hidden="true">
        <Skeleton className={styles.skeletonRow} />
        <Skeleton className={styles.skeletonRow} />
        <Skeleton className={styles.skeletonRow} />
      </div>
    );
  } else {
    const { provider, embedding, autoActions } = data;
    const providerFixable = provider.state === 'none' || provider.state === 'no_default_model';
    const autoOn = autoActions.enabled && autoActions.intervalMinutes > 0;
    body = (
      <ul className={styles.rows}>
        <StatusRow
          label={t('ai_page.status.features.label')}
          hint={t('ai_page.status.features.hint')}
          ok={provider.state === 'ready'}
          stateText={featuresText(provider, t)}
          action={
            providerFixable
              ? admin
                ? fixLink('/workspaces/$id/settings/ai/connection')
                : askAdmin
              : null
          }
        />
        <StatusRow
          label={t('ai_page.status.embedding.label')}
          hint={t('ai_page.status.embedding.hint')}
          ok={embedding.status === 'enabled'}
          stateText={embeddingText(embedding.status, t)}
          action={
            embedding.status === 'disabled'
              ? admin
                ? fixLink('/workspaces/$id/settings/ai/connection')
                : askAdmin
              : null
          }
        />
        <StatusRow
          label={t('ai_page.status.auto_actions.label')}
          hint={t('ai_page.status.auto_actions.hint')}
          ok={autoOn}
          stateText={
            autoOn
              ? t('ai_page.status.auto_actions.on', { minutes: autoActions.intervalMinutes })
              : t('ai_page.status.auto_actions.off')
          }
          action={admin ? fixLink('/workspaces/$id/settings/ai/automation') : null}
        />
      </ul>
    );
  }

  return (
    <section className={styles.card} aria-labelledby="ai-status-title">
      <h2 id="ai-status-title" className={styles.title}>
        {t('ai_page.status.title')}
      </h2>
      {body}
    </section>
  );
}

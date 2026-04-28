/**
 * PublicLensPage — standalone read-only page for viewing a published lens.
 *
 * Accessible at /public/lenses/{token} without authentication. Displays
 * the lens name, description, and a simple task table. No sidebar or
 * navigation — this is a standalone shareable page.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { CircleAlert } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PublicPageLayout from '../../components/public-page-layout';
import { ApiError, isNetworkError } from '../../lib/api-error';
import { usePublicLensQuery } from './api';
import styles from './sharing.module.css';

export interface PublicLensPageProps {
  token: string;
}

/**
 * Treat the same lens-publish lifecycle codes the API surfaces as
 * deterministic terminal states: `EXPIRED` (the publish window closed)
 * and `NOT_FOUND` (the lens was unpublished or the token was always
 * bogus). Anything else routes through the generic invalid copy.
 */
function isExpiredLensError(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'LENS.PUBLIC.EXPIRED';
}

export default function PublicLensPage({ token }: PublicLensPageProps): ReactElement {
  const { t } = useTranslation('sharing');
  const { data, isLoading, error } = usePublicLensQuery(token);

  if (isLoading) {
    return (
      <PublicPageLayout busy mainLabel={t('public_page.loading')}>
        <Skeleton style={{ height: '3rem' }} />
        <Skeleton style={{ height: '4rem' }} />
        <Skeleton style={{ height: '4rem' }} />
      </PublicPageLayout>
    );
  }

  if (error || !data) {
    const network = isNetworkError(error);
    const expired = isExpiredLensError(error);
    const titleKey = network
      ? 'public_page.errors.network_title'
      : expired
        ? 'public_page.errors.expired_title'
        : 'public_page.errors.invalid_title';
    const bodyKey = network
      ? 'public_page.errors.network_body'
      : expired
        ? 'public_page.errors.expired_body'
        : 'public_page.errors.invalid_body';
    return (
      <PublicPageLayout showBrandHeader alignMain="center" mainLabel={t(titleKey)}>
        <CircleAlert
          size={48}
          aria-hidden="true"
          style={{ color: 'var(--nf-color-fg-subtle)', alignSelf: 'center' }}
        />
        <h1
          style={{
            fontSize: 'var(--nf-text-xl)',
            fontWeight: 'var(--nf-weight-semibold)',
            color: 'var(--nf-color-fg)',
            margin: 0,
            textAlign: 'center',
          }}
        >
          {t(titleKey)}
        </h1>
        <p
          style={{
            color: 'var(--nf-color-fg-muted)',
            margin: 0,
            maxInlineSize: 'var(--nf-measure-narrow)',
            alignSelf: 'center',
            textAlign: 'center',
          }}
        >
          {t(bodyKey)}
        </p>
      </PublicPageLayout>
    );
  }

  return (
    <div className={styles.publicPage}>
      <header className={styles.publicHeader}>
        <h1 className={styles.publicTitle}>{data.name}</h1>
        {data.description ? <p className={styles.publicDescription}>{data.description}</p> : null}
      </header>

      <main className={styles.publicContent} aria-label={data.name}>
        {data.tasks.length === 0 ? (
          <p>{t('public_page.no_tasks')}</p>
        ) : (
          <table className={styles.publicTable}>
            <thead>
              <tr>
                <th>{t('public_page.col_title')}</th>
                <th>{t('public_page.col_status')}</th>
                <th>{t('public_page.col_priority')}</th>
                <th>{t('public_page.col_due')}</th>
                <th>{t('public_page.col_assignee')}</th>
              </tr>
            </thead>
            <tbody>
              {data.tasks.map((task) => (
                <tr key={task.id}>
                  <td>{task.title}</td>
                  <td>{task.status}</td>
                  <td>{String(task.priority)}</td>
                  <td>{task.dueOn ?? '—'}</td>
                  <td>{task.assigneeDisplayName ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </main>

      <footer className={styles.publicFooter}>{t('public_page.footer')}</footer>
    </div>
  );
}

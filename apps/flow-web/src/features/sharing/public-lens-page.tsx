/**
 * PublicLensPage — standalone read-only page for viewing a published lens.
 *
 * Accessible at /public/lenses/{token} without authentication. Displays
 * the lens name, description, and a simple task table. No sidebar or
 * navigation — this is a standalone shareable page.
 *
 * Anyone holding the link is an anonymous reader, so the projection names no
 * person: the table carries title, status, priority and due date only.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { CircleAlert } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PublicPageLayout from '../../components/public-page-layout';
import { isNetworkError } from '../../lib/api-error';
import { formatDueDate } from '../../lib/format-date';
import type { TaskDerivedState, TaskPriority } from '../tasks/api';
import { PRIORITY_KEY, STATE_KEY } from '../tasks/constants';
import { usePublicLensQuery } from './api';
import styles from './sharing.module.css';

export interface PublicLensPageProps {
  token: string;
}

export default function PublicLensPage({ token }: PublicLensPageProps): ReactElement {
  const { t, i18n } = useTranslation('sharing');
  // Status and priority labels live in `common`, alongside every other view
  // of a task. A public reader gets the same words a member does.
  const { t: tCommon } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data, isLoading, error } = usePublicLensQuery(token);

  if (isLoading) {
    return (
      <PublicPageLayout busy mainLabel={t('public_page.loading')}>
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '3rem' }} />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '4rem' }} />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ height: '4rem' }} />
      </PublicPageLayout>
    );
  }

  if (error || !data) {
    const network = isNetworkError(error);
    const titleKey = network
      ? 'public_page.errors.network_title'
      : 'public_page.errors.invalid_title';
    const bodyKey = network ? 'public_page.errors.network_body' : 'public_page.errors.invalid_body';
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
        {!data.tasks || data.tasks.length === 0 ? (
          <p>{t('public_page.no_tasks')}</p>
        ) : (
          <table className={styles.publicTable}>
            <thead>
              <tr>
                <th>{t('public_page.col_title')}</th>
                <th>{t('public_page.col_status')}</th>
                <th>{t('public_page.col_priority')}</th>
                <th>{t('public_page.col_due')}</th>
              </tr>
            </thead>
            <tbody>
              {data.tasks.map((task) => {
                // The API sends the raw derived state and a 0-4 priority.
                // Rendering those verbatim showed a Japanese reader
                // "waiting" and "3"; route both through the same label maps
                // the authenticated views use.
                const stateKey = STATE_KEY[task.status as TaskDerivedState];
                const priorityKey = PRIORITY_KEY[task.priority as TaskPriority];
                return (
                  <tr key={task.id}>
                    <td>{task.title}</td>
                    <td>{stateKey ? tCommon(stateKey) : task.status}</td>
                    <td>{priorityKey ? tCommon(priorityKey) : String(task.priority)}</td>
                    <td>{task.dueOn ? formatDueDate(task.dueOn, locale) : '—'}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </main>

      <footer className={styles.publicFooter}>{t('public_page.footer')}</footer>
    </div>
  );
}

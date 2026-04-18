/**
 * PublicLensPage — standalone read-only page for viewing a published lens.
 *
 * Accessible at /public/lenses/{token} without authentication. Displays
 * the lens name, description, and a simple task table. No sidebar or
 * navigation — this is a standalone shareable page.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { usePublicLensQuery } from './api';
import styles from './sharing.module.css';

export interface PublicLensPageProps {
  token: string;
}

export default function PublicLensPage({ token }: PublicLensPageProps): ReactElement {
  const { t } = useTranslation('sharing');
  const { data, isLoading, isError } = usePublicLensQuery(token);

  if (isLoading) {
    return (
      <div className={styles.publicCenter}>
        <p>{t('public_page.loading')}</p>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className={styles.publicCenter}>
        <p className={styles.publicError}>{t('public_page.not_found')}</p>
      </div>
    );
  }

  return (
    <div className={styles.publicPage}>
      <header className={styles.publicHeader}>
        <h1 className={styles.publicTitle}>{data.name}</h1>
        {data.description ? <p className={styles.publicDescription}>{data.description}</p> : null}
      </header>

      <main className={styles.publicContent}>
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
                  <td>{task.dueOn ?? '\u2014'}</td>
                  <td>{task.assigneeDisplayName ?? '\u2014'}</td>
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

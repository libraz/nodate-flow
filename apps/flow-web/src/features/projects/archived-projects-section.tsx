/**
 * ArchivedProjectsSection — archived projects on the Archive page, each
 * with a Restore button. Renders nothing when no project is archived.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { Archive } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import archivedStyles from '../tasks/archived/archived.module.css';
import { type Project, useUnarchiveProject } from './api';
import styles from './archived-projects-section.module.css';

export interface ArchivedProjectsSectionProps {
  workspaceId: string;
  projects: readonly Project[];
}

export default function ArchivedProjectsSection({
  workspaceId,
  projects,
}: ArchivedProjectsSectionProps): ReactElement | null {
  const { t } = useTranslation('archive');
  const { t: tCommon } = useTranslation('common');
  const unarchive = useUnarchiveProject();
  const [busyId, setBusyId] = useState<string | null>(null);

  const archived = projects.filter((p) => p.isArchived);
  if (archived.length === 0) return null;

  const handleRestore = async (project: Project): Promise<void> => {
    setBusyId(project.id);
    try {
      await unarchive.mutateAsync(project.id);
      toaster.show({
        tone: 'success',
        message: tCommon('projects.archive.restored', { name: project.name }),
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, tCommon, 'projects.archive.restore_failed'),
      });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <Card style={{ padding: 'var(--nf-space-5)' }}>
      <section aria-labelledby="archived-projects-title">
        <h2 id="archived-projects-title" className={archivedStyles.chapterTitle}>
          {t('projects.title')}
        </h2>
        <p className={styles.description}>{t('projects.description')}</p>
        <ul className={styles.list}>
          {archived.map((project) => {
            const busy = busyId === project.id;
            return (
              <li key={project.id} className={styles.row}>
                <span className={styles.name}>
                  <Archive size={14} aria-hidden className={styles.icon} />
                  <Link
                    to="/workspaces/$id/projects/$projectId/tasks"
                    params={{ id: workspaceId, projectId: project.id }}
                    className={styles.link}
                  >
                    {project.name}
                  </Link>
                </span>
                <Button
                  variant="default"
                  size="sm"
                  disabled={busy}
                  aria-busy={busy || undefined}
                  onClick={() => {
                    void handleRestore(project);
                  }}
                >
                  {tCommon('projects.archive.restore')}
                </Button>
              </li>
            );
          })}
        </ul>
      </section>
    </Card>
  );
}

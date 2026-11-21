/**
 * ProjectArchivedBanner — quiet notice above the project tabs while the
 * project is archived, with the single action that undoes it.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Archive } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useUnarchiveProject } from './api';
import styles from './project-archived-banner.module.css';

export interface ProjectArchivedBannerProps {
  projectId: string;
  projectName: string;
  isArchived: boolean;
}

export default function ProjectArchivedBanner({
  projectId,
  projectName,
  isArchived,
}: ProjectArchivedBannerProps): ReactElement | null {
  const { t } = useTranslation('common');
  const unarchive = useUnarchiveProject();
  if (!isArchived) return null;

  const handleRestore = async (): Promise<void> => {
    try {
      await unarchive.mutateAsync(projectId);
      toaster.show({
        tone: 'success',
        message: t('projects.archive.restored', { name: projectName }),
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.archive.restore_failed'),
      });
    }
  };

  return (
    <div className={styles.banner}>
      <Archive size={16} aria-hidden className={styles.icon} />
      <p className={styles.text}>{t('projects.archived_banner.text')}</p>
      <Button
        variant="default"
        size="sm"
        className={styles.action}
        disabled={unarchive.isPending}
        aria-busy={unarchive.isPending || undefined}
        onClick={() => {
          void handleRestore();
        }}
      >
        {t('projects.archive.restore')}
      </Button>
    </div>
  );
}

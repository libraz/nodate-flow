/**
 * ProjectArchiveActions — the "Tidy up" card on the project settings tab.
 *
 * Archiving is reversible, so it runs without a confirmation dialog and
 * offers Undo from the success toast instead.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useArchiveProject, useProjectQuery, useUnarchiveProject } from './api';

export default function ProjectArchiveActions({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: project } = useProjectQuery(id);
  const archive = useArchiveProject();
  const unarchive = useUnarchiveProject();
  const { name, isArchived } = project;
  const pending = archive.isPending || unarchive.isPending;

  const handleRestore = async (): Promise<void> => {
    try {
      await unarchive.mutateAsync(id);
      toaster.show({ tone: 'success', message: t('projects.archive.restored', { name }) });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.archive.restore_failed'),
      });
    }
  };

  const handleArchive = async (): Promise<void> => {
    try {
      await archive.mutateAsync(id);
      toaster.show({
        tone: 'success',
        message: t('projects.archive.done', { name }),
        action: {
          label: t('common.undo'),
          onAction: () => {
            void handleRestore();
          },
        },
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.archive.failed'),
      });
    }
  };

  return (
    <Card style={{ padding: 'var(--nf-space-5)' }}>
      <section>
        <h3 className="text-base font-semibold text-[var(--nf-color-fg)]">
          {t('projects.archive.section_title')}
        </h3>
        <p className="text-sm text-[var(--nf-color-fg-muted)] mb-4">
          {t('projects.archive.description')}
        </p>
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button
            variant="default"
            disabled={pending}
            aria-busy={pending || undefined}
            onClick={() => {
              void (isArchived ? handleRestore() : handleArchive());
            }}
          >
            {isArchived ? t('projects.archive.restore') : t('projects.archive.action')}
          </Button>
        </div>
      </section>
    </Card>
  );
}

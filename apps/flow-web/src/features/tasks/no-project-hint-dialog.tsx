/**
 * NoProjectHintDialog — shown when "new task" is triggered while no
 * project is reachable. Offers the next step that matches who is asking:
 * create a workspace, create a project (admins), or ask an admin.
 */

import Button, { buttonClassName } from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { Link } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import ProjectCreateDialog from '../projects/project-create-dialog';

export interface NoProjectHintDialogProps {
  open: boolean;
  onClose: () => void;
  workspaceId: string | null;
  canCreateProject: boolean;
  onProjectCreated: (projectId: string) => void;
}

const bodyStyle = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--nf-space-4)',
  // nf-token-override: component dimension, not a spacing step
  minInlineSize: '20rem',
} as const;

const actionsStyle = {
  display: 'flex',
  gap: 'var(--nf-space-2)',
  justifyContent: 'flex-end',
} as const;

export default function NoProjectHintDialog({
  open,
  onClose,
  workspaceId,
  canCreateProject,
  onProjectCreated,
}: NoProjectHintDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const [createOpen, setCreateOpen] = useState(false);

  const canCreate = workspaceId !== null && canCreateProject;
  const bodyKey =
    workspaceId === null
      ? 'tasks.quick_create.no_workspace.title'
      : canCreate
        ? 'tasks.quick_create.no_projects.title'
        : 'tasks.quick_create.no_projects.member_body';

  return (
    <>
      <Dialog open={open && !createOpen} onClose={onClose} title={t('tasks.new')}>
        <div style={bodyStyle}>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t(bodyKey)}</p>
          <div style={actionsStyle}>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t('tasks.form.cancel')}
            </Button>
            {workspaceId === null ? (
              <Link
                to="/setup"
                onClick={onClose}
                className={buttonClassName({ variant: 'primary' })}
              >
                {t('nav.noWorkspace.cta')}
              </Link>
            ) : canCreate ? (
              <Button
                type="button"
                variant="primary"
                onClick={() => {
                  setCreateOpen(true);
                }}
              >
                {t('tasks.quick_create.no_projects.cta')}
              </Button>
            ) : null}
          </div>
        </div>
      </Dialog>
      {canCreate ? (
        <ProjectCreateDialog
          workspaceId={workspaceId}
          open={open && createOpen}
          onClose={() => {
            setCreateOpen(false);
          }}
          onCreated={(project) => {
            setCreateOpen(false);
            onProjectCreated(project.id);
          }}
        />
      ) : null}
    </>
  );
}

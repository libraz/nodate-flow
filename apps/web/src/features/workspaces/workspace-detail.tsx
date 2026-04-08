/**
 * WorkspaceDetail — header + tabs (Overview / Members / Settings).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useDisableWorkspace, useUpdateWorkspace, useWorkspaceQuery } from './api';
import WorkspaceMembersTable from './workspace-members-table';

export interface WorkspaceDetailProps {
  id: string;
}

function SettingsPanel({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(id);
  const update = useUpdateWorkspace();
  const disable = useDisableWorkspace();
  const navigate = useNavigate();

  const [name, setName] = useState(workspace.name);
  const [submitting, setSubmitting] = useState(false);

  const handleRename = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (name.trim() === '' || name === workspace.name) return;
    setSubmitting(true);
    try {
      await update.mutateAsync({ id, patch: { name } });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.errors.update_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDisable = async (): Promise<void> => {
    if (!window.confirm(t('workspaces.settings.disable_confirm'))) return;
    try {
      await disable.mutateAsync(id);
      void navigate({ to: '/workspaces' });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.errors.disable_failed') });
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
      <Card style={{ padding: '1.25rem' }}>
        <form
          onSubmit={(e) => {
            void handleRename(e);
          }}
          style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
        >
          <FormField label={t('workspaces.form.name')} required>
            {(control) => (
              <Input
                {...control}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
              />
            )}
          </FormField>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Button type="submit" variant="primary" disabled={submitting}>
              {t('workspaces.settings.rename')}
            </Button>
          </div>
        </form>
      </Card>

      <Card style={{ padding: '1.25rem' }}>
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button
            variant="danger"
            onClick={() => {
              void handleDisable();
            }}
          >
            {t('workspaces.settings.disable')}
          </Button>
        </div>
      </Card>
    </div>
  );
}

function OverviewPanel(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Card style={{ padding: '1.5rem' }}>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>
        {t('workspaces.detail.overview_empty')}
      </p>
    </Card>
  );
}

export default function WorkspaceDetail({ id }: WorkspaceDetailProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspace } = useWorkspaceQuery(id);

  const items: TabItem[] = [
    {
      value: 'overview',
      label: t('workspaces.detail.tabs.overview'),
      content: (
        <Suspense fallback={null}>
          <OverviewPanel />
        </Suspense>
      ),
    },
    {
      value: 'members',
      label: t('workspaces.detail.tabs.members'),
      content: (
        <Suspense fallback={null}>
          <WorkspaceMembersTable workspaceId={id} />
        </Suspense>
      ),
    },
    {
      value: 'settings',
      label: t('workspaces.detail.tabs.settings'),
      content: (
        <Suspense fallback={null}>
          <SettingsPanel id={id} />
        </Suspense>
      ),
    },
  ];

  const headingId = useId();

  return (
    <article
      aria-labelledby={headingId}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          id={headingId}
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
            margin: 0,
          }}
        >
          {workspace.name}
        </h1>
        {workspace.description ? (
          <p style={{ margin: 0, color: 'var(--color-muted)' }}>{workspace.description}</p>
        ) : null}
        <nav
          aria-label={t('workspaces.detail.projects_nav_label')}
          style={{ display: 'flex', gap: '1rem' }}
        >
          <Link
            to="/workspaces/$id/projects"
            params={{ id }}
            style={{ color: 'var(--color-accent)' }}
          >
            {t('projects.title')}
          </Link>
        </nav>
      </header>

      <Tabs items={items} defaultValue="overview" aria-label={t('workspaces.detail.tabs.label')} />
    </article>
  );
}

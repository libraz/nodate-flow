/**
 * ProjectDetail — header + tabs (Overview / Members / Settings).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useDisableProject, useProjectQuery, useUpdateProject } from './api';
import ProjectMembersTable from './project-members-table';

export interface ProjectDetailProps {
  id: string;
}

function SettingsPanel({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: project } = useProjectQuery(id);
  const update = useUpdateProject();
  const disable = useDisableProject();
  const navigate = useNavigate();

  const [name, setName] = useState(project.name);
  const [submitting, setSubmitting] = useState(false);

  const handleRename = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (name.trim() === '' || name === project.name) return;
    setSubmitting(true);
    try {
      await update.mutateAsync({ id, patch: { name } });
    } catch {
      toaster.show({ tone: 'danger', message: t('projects.errors.update_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDisable = async (): Promise<void> => {
    if (!window.confirm(t('projects.settings.disable_confirm'))) return;
    try {
      await disable.mutateAsync(id);
      void navigate({ to: '/workspaces' });
    } catch {
      toaster.show({ tone: 'danger', message: t('projects.errors.disable_failed') });
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
          <FormField label={t('projects.form.name')} required>
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
              {t('projects.settings.rename')}
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
            {t('projects.settings.disable')}
          </Button>
        </div>
      </Card>
    </div>
  );
}

function OverviewPanel(_props: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <Card style={{ padding: '1.5rem' }}>
      <p style={{ margin: 0, color: 'var(--color-muted)' }}>
        {t('projects.detail.overview_empty')}
      </p>
    </Card>
  );
}

export default function ProjectDetail({ id }: ProjectDetailProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: project } = useProjectQuery(id);

  const items: TabItem[] = [
    {
      value: 'overview',
      label: t('projects.detail.tabs.overview'),
      content: (
        <Suspense fallback={null}>
          <OverviewPanel id={id} />
        </Suspense>
      ),
    },
    {
      value: 'members',
      label: t('projects.detail.tabs.members'),
      content: (
        <Suspense fallback={null}>
          <ProjectMembersTable projectId={id} />
        </Suspense>
      ),
    },
    {
      value: 'settings',
      label: t('projects.detail.tabs.settings'),
      content: (
        <Suspense fallback={null}>
          <SettingsPanel id={id} />
        </Suspense>
      ),
    },
  ];

  return (
    <section
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
            margin: 0,
          }}
        >
          {project.name}
        </h1>
        {project.description ? (
          <p style={{ margin: 0, color: 'var(--color-muted)' }}>{project.description}</p>
        ) : null}
      </header>

      <Tabs items={items} defaultValue="overview" aria-label={t('projects.detail.tabs.overview')} />
    </section>
  );
}

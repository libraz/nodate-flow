/**
 * ProjectDetail — header + tabs (Overview / Members / Settings).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Tabs, { type TabItem } from '@nodate-flow/ui/primitives/tabs';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { type TaskDerivedState, useTasksQuery } from '../tasks/api';
import { STATE_COLOR } from '../tasks/constants';
import { useDisableProject, useProjectQuery, useUpdateProject } from './api';
import ProjectMembersTable from './project-members-table';

const STATE_ORDER: readonly TaskDerivedState[] = ['open', 'waiting', 'review', 'done', 'cancelled'];

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
    if (!(await confirmAction({ message: t('projects.settings.disable_confirm') }))) return;
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

function OverviewPanel({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { data: tasks } = useTasksQuery(id);

  if (tasks.length === 0) {
    return (
      <Card style={{ padding: '1.5rem' }}>
        <p
          style={{
            margin: 0,
            padding: '2rem 1rem',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted, var(--color-muted))',
            border: '1px dashed var(--nf-color-border, var(--color-border))',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-bg-sunken, transparent)',
          }}
        >
          {t('projects.detail.overview_empty')}
        </p>
      </Card>
    );
  }

  const counts: Record<TaskDerivedState, number> = {
    open: 0,
    waiting: 0,
    review: 0,
    done: 0,
    cancelled: 0,
  };
  for (const task of tasks) {
    const state = task.derivedState as TaskDerivedState;
    if (state in counts) counts[state] += 1;
  }
  const active = counts.open + counts.waiting + counts.review;
  const total = tasks.length;
  const pct = total > 0 ? Math.round((counts.done / total) * 100) : 0;

  return (
    <Card style={{ padding: '1.5rem', display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: '1rem', flexWrap: 'wrap' }}>
        <div>
          <div style={{ fontSize: '2rem', fontWeight: 600, lineHeight: 1 }}>{active}</div>
          <div style={{ fontSize: '0.8125rem', color: 'var(--color-muted)' }}>
            {t('projects.detail.active_tasks')}
          </div>
        </div>
        <div
          style={{
            marginInlineStart: 'auto',
            fontSize: '0.875rem',
            color: 'var(--color-muted)',
          }}
        >
          {t('projects.detail.progress', { pct })}
        </div>
      </div>
      <div
        style={{
          display: 'flex',
          blockSize: '0.5rem',
          borderRadius: '999px',
          overflow: 'hidden',
          background: 'var(--color-surface, rgba(127,127,127,0.08))',
        }}
      >
        {STATE_ORDER.map((s) =>
          counts[s] > 0 ? (
            <div
              key={s}
              title={`${t(`tasks.status.${s}`)}: ${counts[s]}`}
              style={{
                flex: `${counts[s]} 1 0`,
                background: STATE_COLOR[s],
              }}
            />
          ) : null,
        )}
      </div>
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(8rem, 1fr))',
          gap: '0.5rem',
        }}
      >
        {STATE_ORDER.map((s) => (
          <li
            key={s}
            style={{
              padding: '0.5rem 0.75rem',
              borderRadius: '0.5rem',
              background: 'var(--color-surface, rgba(127,127,127,0.05))',
              display: 'flex',
              flexDirection: 'column',
              gap: '0.125rem',
            }}
          >
            <span style={{ fontSize: '0.75rem', color: 'var(--color-muted)' }}>
              {t(`tasks.status.${s}`)}
            </span>
            <span
              style={{ fontSize: '1.25rem', fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}
            >
              {counts[s]}
            </span>
          </li>
        ))}
      </ul>
      <Link
        to="/projects/$projectId/tasks"
        params={{ projectId: id }}
        style={{
          alignSelf: 'flex-start',
          fontSize: '0.875rem',
          color: 'var(--color-accent, #9b59b6)',
          textDecoration: 'none',
        }}
      >
        {t('projects.detail.view_all_tasks')} →
      </Link>
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

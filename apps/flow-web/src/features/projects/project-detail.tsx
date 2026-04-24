/**
 * ProjectDetail — header + tabs (Overview / Members / Settings).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Switch from '@nodate-flow/ui/primitives/switch';
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

/** Identifiers for the project-detail tab panels. */
export type ProjectDetailTab = 'overview' | 'members' | 'settings';

export interface ProjectDetailProps {
  id: string;
  /** Controlled active tab, typically driven by the `?tab=` search param. */
  tab: ProjectDetailTab;
  /** Called when the user activates a different tab. Consumers persist the value. */
  onTabChange: (tab: ProjectDetailTab) => void;
}

/** Feature flag keys exposed on the project resource. */
type FeatureFlag = 'featurePages' | 'featureTimeboxes' | 'featureLenses' | 'featureCalendar';

/** i18n label key suffix for each feature flag. */
const FEATURE_TOGGLES: readonly { flag: FeatureFlag; labelKey: string }[] = [
  { flag: 'featurePages', labelKey: 'pages' },
  { flag: 'featureTimeboxes', labelKey: 'timeboxes' },
  { flag: 'featureLenses', labelKey: 'lenses' },
  { flag: 'featureCalendar', labelKey: 'calendar' },
];

function SettingsPanel({ id }: { id: string }): ReactElement {
  const { t } = useTranslation('common');
  const { t: tLabels } = useTranslation('labels');
  const { data: project } = useProjectQuery(id);
  const update = useUpdateProject();
  const disable = useDisableProject();
  const navigate = useNavigate();

  const [name, setName] = useState(project.name);
  const [submitting, setSubmitting] = useState(false);

  const trimmedName = name.trim();
  const renameDisabled = submitting || trimmedName === '' || trimmedName === project.name;

  const handleRename = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (renameDisabled) return;
    setSubmitting(true);
    try {
      await update.mutateAsync({ id, patch: { name: trimmedName } });
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

  /* The project object may carry feature flags that the SDK types haven't
     picked up yet — cast to a record for safe access. The backend's
     PatchProjectBody does NOT accept these fields, so the switches are
     rendered read-only. A "coming soon" subtitle on the section header
     signals the intent instead of firing a request the server would
     silently drop. */
  const featureFlags = project as unknown as Record<FeatureFlag, boolean | undefined>;

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
            <Button type="submit" variant="primary" disabled={renameDisabled}>
              {t('projects.settings.rename')}
            </Button>
          </div>
        </form>
      </Card>

      <Card style={{ padding: '1.25rem' }}>
        <section>
          <div className="flex items-baseline gap-2 flex-wrap">
            <h3 className="text-base font-semibold text-[var(--nf-color-fg)]">
              {tLabels('feature_toggles.title')}
            </h3>
            <small className="text-xs text-[var(--nf-color-fg-muted)]">
              {tLabels('feature_toggles.coming_soon_subtitle')}
            </small>
          </div>
          <p className="text-sm text-[var(--nf-color-fg-muted)] mb-4">
            {tLabels('feature_toggles.description')}
          </p>
          <div className="flex flex-col gap-4">
            {FEATURE_TOGGLES.map(({ flag, labelKey }) => (
              <div key={flag} className="flex items-center justify-between gap-4">
                <div>
                  <span className="text-sm font-medium text-[var(--nf-color-fg)]">
                    {tLabels(`feature_toggles.${labelKey}`)}
                  </span>
                  <p className="text-xs text-[var(--nf-color-fg-muted)]">
                    {tLabels(`feature_toggles.${labelKey}_description`)}
                  </p>
                </div>
                <Switch
                  checked={featureFlags[flag] ?? false}
                  disabled
                  aria-label={tLabels(`feature_toggles.${labelKey}`)}
                />
              </div>
            ))}
          </div>
        </section>
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

function OverviewPanel({
  id,
  workspaceId,
}: {
  id: string;
  workspaceId: string;
}): ReactElement {
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
            color: 'var(--nf-color-fg-muted, var(--nf-color-fg-muted))',
            border: '1px dashed var(--nf-color-border, var(--nf-color-border))',
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
          <div style={{ fontSize: '0.8125rem', color: 'var(--nf-color-fg-muted)' }}>
            {t('projects.detail.active_tasks')}
          </div>
        </div>
        <div
          style={{
            marginInlineStart: 'auto',
            fontSize: '0.875rem',
            color: 'var(--nf-color-fg-muted)',
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
          background: 'var(--nf-color-surface))',
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
              background: 'var(--nf-color-surface))',
              display: 'flex',
              flexDirection: 'column',
              gap: '0.125rem',
            }}
          >
            <span style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>
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
        to="/workspaces/$id/projects/$projectId/tasks"
        params={{ id: workspaceId, projectId: id }}
        style={{
          alignSelf: 'flex-start',
          fontSize: '0.875rem',
          color: 'var(--nf-color-accent)',
          textDecoration: 'none',
        }}
      >
        {t('projects.detail.view_all_tasks')} →
      </Link>
    </Card>
  );
}

export default function ProjectDetail({ id, tab, onTabChange }: ProjectDetailProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: project } = useProjectQuery(id);

  const items: TabItem[] = [
    {
      value: 'overview',
      label: t('projects.detail.tabs.overview'),
      content: (
        <Suspense fallback={null}>
          <OverviewPanel id={id} workspaceId={project.workspaceId} />
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
      <Tabs
        items={items}
        value={tab}
        onValueChange={(next) => {
          onTabChange(next as ProjectDetailTab);
        }}
        aria-label={t('projects.detail.tabs.overview')}
      />
    </section>
  );
}

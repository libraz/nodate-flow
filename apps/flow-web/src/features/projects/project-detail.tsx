/**
 * Project detail panels — Overview and Settings. Each is mounted by its own
 * route under the project layout (Members lives in `project-members-table`).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, useNavigate } from '@tanstack/react-router';
import { type CSSProperties, type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { type TaskDerivedState, useTasksQuery } from '../tasks/api';
import { STATE_COLOR } from '../tasks/constants';
import StateDot from '../tasks/state-dot';
import listStyles from '../workspaces/workspaces.module.css';
import { useDisableProject, useProjectQuery, useUpdateProject } from './api';
import ProjectArchiveActions from './project-archive-actions';
import styles from './projects.module.css';

const STATE_ORDER: readonly TaskDerivedState[] = ['open', 'waiting', 'review', 'done', 'cancelled'];

/** Fraction of all tasks in `state` and every state before it. */
function cumulativeShare(
  counts: Record<TaskDerivedState, number>,
  state: TaskDerivedState,
  total: number,
): number {
  if (total === 0) return 0;
  let sum = 0;
  for (const s of STATE_ORDER) {
    sum += counts[s];
    if (s === state) break;
  }
  return sum / total;
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

export function SettingsPanel({ id }: { id: string }): ReactElement {
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
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.errors.update_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDisable = async (): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('projects.settings.disable_confirm'),
      confirmLabel: t('projects.settings.disable'),
      tone: 'danger',
    });
    if (!confirmed) return;
    try {
      await disable.mutateAsync(id);
      void navigate({ to: '/workspaces' });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.errors.disable_failed'),
      });
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
    <div className={styles.settings}>
      <Card className={styles.settingsCard}>
        <form
          onSubmit={(e) => {
            void handleRename(e);
          }}
          className={styles.settingsForm}
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
          <div className={styles.actionsEnd}>
            <Button
              type="submit"
              variant="primary"
              disabled={renameDisabled}
              aria-busy={submitting || undefined}
            >
              {t('projects.settings.rename')}
            </Button>
          </div>
        </form>
      </Card>

      <Card className={styles.settingsCard}>
        <section>
          <div className={styles.sectionHead}>
            <h3 className={styles.sectionTitle}>{tLabels('feature_toggles.title')}</h3>
            <small className={styles.sectionSubtitle}>
              {tLabels('feature_toggles.coming_soon_subtitle')}
            </small>
          </div>
          <p className={styles.sectionDescription}>{tLabels('feature_toggles.description')}</p>
          <div className={styles.toggleList}>
            {FEATURE_TOGGLES.map(({ flag, labelKey }) => (
              <div key={flag} className={styles.toggleRow}>
                <div>
                  <span className={styles.toggleLabel}>
                    {tLabels(`feature_toggles.${labelKey}`)}
                  </span>
                  <p className={styles.toggleHint}>
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

      <ProjectArchiveActions id={id} />

      <Card className={styles.settingsCard}>
        <section>
          <h3 className={styles.sectionTitle}>{t('projects.settings.danger_title')}</h3>
          <p className={styles.sectionDescription}>{t('projects.settings.disable_description')}</p>
          <div className={styles.actionsEnd}>
            <Button
              variant="danger"
              onClick={() => {
                void handleDisable();
              }}
            >
              {t('projects.settings.disable')}
            </Button>
          </div>
        </section>
      </Card>
    </div>
  );
}

export function OverviewPanel({
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
      <Card>
        <EmptyState titleAs="p" title={t('projects.detail.overview_empty')} />
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
    <Card className={styles.overview}>
      <div className={styles.summary}>
        <div>
          <div className={styles.activeCount}>{active}</div>
          <div className={styles.caption}>{t('projects.detail.active_tasks')}</div>
        </div>
        <div className={styles.progress}>{t('projects.detail.progress', { pct })}</div>
      </div>
      <div className={styles.bar}>
        {/* Layers are cumulative and painted back to front, so a count change scales them instead of re-flowing a flex row. */}
        {[...STATE_ORDER].reverse().map((s) => (
          <div
            key={s}
            className={styles.segment}
            title={`${t(`tasks.status.${s}`)}: ${counts[s]}`}
            style={
              {
                '--share': cumulativeShare(counts, s, total),
                background: STATE_COLOR[s],
              } as CSSProperties
            }
          />
        ))}
      </div>
      <ul className={styles.states}>
        {STATE_ORDER.map((s) => (
          <li key={s} className={styles.state}>
            <span className={styles.stateLabel}>
              <StateDot state={s} />
              {t(`tasks.status.${s}`)}
            </span>
            <span className={styles.stateCount}>{counts[s]}</span>
          </li>
        ))}
      </ul>
      <Link
        to="/workspaces/$id/projects/$projectId/tasks"
        params={{ id: workspaceId, projectId: id }}
        className={listStyles.viewAll}
      >
        {t('projects.detail.view_all_tasks')} <span aria-hidden>→</span>
      </Link>
    </Card>
  );
}

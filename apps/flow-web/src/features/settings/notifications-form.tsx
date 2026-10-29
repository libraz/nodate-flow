/**
 * NotificationsForm — mute or unmute the caller's notification
 * categories for one workspace. Parent must wrap this in `<Suspense>`
 * because it consumes Suspense-mode queries.
 *
 * Preferences are stored per workspace, so the form picks a workspace
 * first and every switch below applies to that one.
 *
 * Only the in-app channel is offered. The stored model and the API also
 * carry email and push, but nothing in the product delivers on those
 * channels yet, and a switch that reports success while delivering
 * nothing is precisely the failure this screen used to have.
 */

import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useSubmitGuard } from '../../lib/use-submit-guard';
import { useWorkspacesQuery } from '../workspaces/api';
import {
  type NotificationPreference,
  useNotificationPreferencesQuery,
  useUpdateNotificationPreferences,
} from './notifications-api';
import styles from './notifications-form.module.css';
import SaveStatus, { useSaveStatus } from './save-status';
import ui from './settings-ui.module.css';

/** The channel the form edits; see the module comment. */
const CHANNEL = 'in_app';

interface CategoryRow {
  /** Value of `eventCategory` on the wire. */
  readonly category: string;
  readonly labelKey: string;
  readonly descriptionKey: string;
}

/**
 * Categories in the order the API returns them. The list mirrors the
 * server's category set; a category present there but missing here is
 * simply not editable, so the two must be kept in step.
 */
const CATEGORIES: readonly CategoryRow[] = [
  {
    category: 'task.lifecycle',
    labelKey: 'notifications.categories.task_lifecycle.label',
    descriptionKey: 'notifications.categories.task_lifecycle.description',
  },
  {
    category: 'task.comment',
    labelKey: 'notifications.categories.task_comment.label',
    descriptionKey: 'notifications.categories.task_comment.description',
  },
  {
    category: 'task.mention',
    labelKey: 'notifications.categories.task_mention.label',
    descriptionKey: 'notifications.categories.task_mention.description',
  },
  {
    category: 'relation',
    labelKey: 'notifications.categories.relation.label',
    descriptionKey: 'notifications.categories.relation.description',
  },
  {
    category: 'timebox',
    labelKey: 'notifications.categories.timebox.label',
    descriptionKey: 'notifications.categories.timebox.description',
  },
  {
    category: 'ai',
    labelKey: 'notifications.categories.ai.label',
    descriptionKey: 'notifications.categories.ai.description',
  },
] as const;

/**
 * mutedByCategory reduces the server matrix to the in-app column. A
 * category the server did not report is treated as delivering, which
 * matches the server-side default for the in-app channel.
 */
function mutedByCategory(preferences: readonly NotificationPreference[]): Record<string, boolean> {
  const state: Record<string, boolean> = {};
  for (const row of CATEGORIES) state[row.category] = false;
  for (const pref of preferences) {
    if (pref.channel !== CHANNEL) continue;
    if (!(pref.eventCategory in state)) continue;
    state[pref.eventCategory] = pref.muted;
  }
  return state;
}

/** Panel bound to one workspace; remounted when the workspace changes. */
function WorkspacePreferences({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const { data: preferences } = useNotificationPreferencesQuery(workspaceId);
  const update = useUpdateNotificationPreferences(workspaceId);
  const submitGuard = useSubmitGuard();

  const serverState = useMemo(() => mutedByCategory(preferences), [preferences]);
  const [muted, setMuted] = useState<Record<string, boolean>>(serverState);
  // What was last loaded or saved; the refetch after a save lands later.
  const [baseline, setBaseline] = useState<Record<string, boolean>>(serverState);
  const dirty = CATEGORIES.some(
    (row) => (muted[row.category] ?? false) !== (baseline[row.category] ?? false),
  );
  const saveStatus = useSaveStatus(dirty);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (submitGuard.guard()) return;
    const body: NotificationPreference[] = CATEGORIES.map((row) => ({
      eventCategory: row.category,
      channel: CHANNEL,
      muted: muted[row.category] ?? false,
    }));
    try {
      await update.mutateAsync(body);
      setBaseline(muted);
      saveStatus.markSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'notifications.errors.update_failed'),
      });
    } finally {
      submitGuard.end();
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      className={styles.form}
    >
      <ul className={styles.list} aria-describedby="notif-channel-note">
        {CATEGORIES.map((row) => {
          const id = `notif-${row.category}`;
          return (
            <li key={row.category} className={styles.row}>
              <div className={styles.identity}>
                <label htmlFor={id} className={styles.label}>
                  {t(row.labelKey)}
                </label>
                <span id={`${id}-help`} className={styles.helpText}>
                  {t(row.descriptionKey)}
                </span>
              </div>
              <Switch
                id={id}
                aria-describedby={`${id}-help`}
                checked={!(muted[row.category] ?? false)}
                onCheckedChange={(next) => {
                  setMuted((prev) => ({ ...prev, [row.category]: !next }));
                }}
                aria-label={t(row.labelKey)}
              />
            </li>
          );
        })}
      </ul>

      <p id="notif-channel-note" className={styles.channelNote}>
        {t('notifications.channel_note')}
      </p>
      <div className={ui.actions}>
        <SaveStatus saved={saveStatus.saved} dirty={dirty} />
        <Button
          type="submit"
          variant="primary"
          disabled={submitGuard.submitting}
          aria-busy={submitGuard.submitting}
        >
          {t('notifications.save')}
        </Button>
      </div>
    </form>
  );
}

function PreferencesSkeleton(): ReactElement {
  return (
    <div className={styles.list} aria-hidden="true">
      {CATEGORIES.map((row) => (
        <div key={row.category} className={styles.row}>
          <div className={styles.identity}>
            <Skeleton className={styles.skeletonLabel} />
            <Skeleton className={styles.skeletonHelp} />
          </div>
          <Skeleton className={styles.skeletonSwitch} />
        </div>
      ))}
    </div>
  );
}

/** Placeholder with the panel's own shape, shown while the queries suspend. */
export function NotificationsFormSkeleton(): ReactElement {
  return (
    <div className={styles.panel}>
      <PreferencesSkeleton />
    </div>
  );
}

export default function NotificationsForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: workspaces } = useWorkspacesQuery();
  const [workspaceId, setWorkspaceId] = useState<string>(workspaces[0]?.id ?? '');

  if (workspaces.length === 0) {
    return <EmptyState titleAs="p" title={t('notifications.no_workspaces')} className={ui.empty} />;
  }

  const selected = workspaces.some((w) => w.id === workspaceId)
    ? workspaceId
    : (workspaces[0]?.id ?? '');

  return (
    <div className={styles.panel}>
      {workspaces.length > 1 && (
        <FormField label={t('notifications.workspace_label')} className={styles.workspacePicker}>
          {(control) => (
            <Select
              {...control}
              value={selected}
              onChange={(e) => {
                setWorkspaceId(e.target.value);
              }}
            >
              {workspaces.map((workspace) => (
                <option key={workspace.id} value={workspace.id}>
                  {workspace.name}
                </option>
              ))}
            </Select>
          )}
        </FormField>
      )}
      {/* Keyed on the workspace so switching resets local edits to that
          workspace's saved state instead of carrying them across. */}
      <Suspense fallback={<PreferencesSkeleton />}>
        <WorkspacePreferences key={selected} workspaceId={selected} />
      </Suspense>
    </div>
  );
}

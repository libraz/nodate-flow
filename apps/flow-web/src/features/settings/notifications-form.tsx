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
import Select from '@nodate-flow/ui/primitives/select';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useSubmitGuard } from '../../lib/use-submit-guard';
import { useWorkspacesQuery } from '../workspaces/api';
import {
  type NotificationPreference,
  useNotificationPreferencesQuery,
  useUpdateNotificationPreferences,
} from './notifications-api';
import styles from './notifications-form.module.css';

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
      toaster.show({ tone: 'success', message: t('notifications.saved') });
    } catch {
      toaster.show({ tone: 'danger', message: t('notifications.errors.update_failed') });
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
      <p className={styles.channelNote}>{t('notifications.channel_note')}</p>
      <ul className={styles.list}>
        {CATEGORIES.map((row) => {
          const id = `notif-${row.category}`;
          return (
            <li key={row.category} className={styles.row}>
              <div className={styles.identity}>
                <label htmlFor={id} className={styles.label}>
                  {t(row.labelKey)}
                </label>
                <span className={styles.helpText}>{t(row.descriptionKey)}</span>
              </div>
              <Switch
                id={id}
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

      <div className={styles.actions}>
        <Button type="submit" variant="primary" disabled={submitGuard.submitting}>
          {submitGuard.submitting ? t('notifications.saving') : t('notifications.save')}
        </Button>
      </div>
    </form>
  );
}

export default function NotificationsForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: workspaces } = useWorkspacesQuery();
  const [workspaceId, setWorkspaceId] = useState<string>(workspaces[0]?.id ?? '');

  if (workspaces.length === 0) {
    return <p className={styles.helpText}>{t('notifications.no_workspaces')}</p>;
  }

  const selected = workspaces.some((w) => w.id === workspaceId)
    ? workspaceId
    : (workspaces[0]?.id ?? '');

  return (
    <div className={styles.panel}>
      {workspaces.length > 1 && (
        <label className={styles.workspacePicker} htmlFor="notif-workspace">
          <span className={styles.helpText}>{t('notifications.workspace_label')}</span>
          <Select
            id="notif-workspace"
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
        </label>
      )}
      {/* Keyed on the workspace so switching resets local edits to that
          workspace's saved state instead of carrying them across. */}
      <WorkspacePreferences key={selected} workspaceId={selected} />
    </div>
  );
}

/**
 * @file Persistent app-shell bar that surfaces the currently-running
 * timebox (if any). Mounts directly under the top bar so the user can
 * see — and stop — their in-flight session from any route.
 *
 * Behaviour:
 *   - Self-fetches the actor's workspaces and fans out a list query
 *     per workspace via `useActiveTimeboxesQuery`.
 *   - Renders only when at least one workspace has a timebox in the
 *     `active` state. Returns `null` otherwise so the slot collapses
 *     and consumes no vertical space.
 *   - When several workspaces have an active timebox, picks the most
 *     recently updated (`updatedAt` desc, tiebreak on `createdAt`).
 *   - Stop action transitions the timebox to `completed` after a
 *     confirmation prompt, then invalidates so the bar collapses.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { type Workspace, useWorkspacesQuery } from '../workspaces/api';
import styles from './active-timebox-bar.module.css';
import {
  type ActiveTimeboxRow,
  useActiveTimeboxesQuery,
  useUpdateTimeboxStatusMutation,
} from './api';

/**
 * Pick the most recently-updated active timebox out of a non-empty
 * list. Tiebreaks on `createdAt` so ordering is stable when multiple
 * rows share a single update second.
 */
function pickPrimary(rows: readonly ActiveTimeboxRow[]): ActiveTimeboxRow | null {
  if (rows.length === 0) return null;
  let best = rows[0];
  if (!best) return null;
  for (let i = 1; i < rows.length; i += 1) {
    const candidate = rows[i];
    if (!candidate) continue;
    if (candidate.timebox.updatedAt > best.timebox.updatedAt) {
      best = candidate;
      continue;
    }
    if (
      candidate.timebox.updatedAt === best.timebox.updatedAt &&
      candidate.timebox.createdAt > best.timebox.createdAt
    ) {
      best = candidate;
    }
  }
  return best ?? null;
}

/**
 * Inner component that runs after `useWorkspacesQuery` has resolved.
 * Split out so the bar can be Suspense-wrapped in the app shell.
 */
function ActiveTimeboxBarInner({ workspaces }: { workspaces: Workspace[] }): ReactElement | null {
  const { t } = useTranslation('common');
  const updateStatus = useUpdateTimeboxStatusMutation();
  const workspaceIds = workspaces.map((w) => w.id);
  const { active } = useActiveTimeboxesQuery(workspaceIds);
  const primary = pickPrimary(active);
  if (!primary) return null;

  const workspaceName =
    workspaces.find((w) => w.id === primary.workspaceId)?.name ?? primary.workspaceId;
  // Total / completed task counters are not part of the timebox list
  // payload — the API surfaces them only via the per-timebox tasks
  // endpoint. The bar shows the timebox name + workspace caption and
  // omits a progress chip rather than triggering N+1 fetches across
  // every active timebox.

  const handleStop = async (): Promise<void> => {
    const ok = await confirmAction({
      message: t('active_timebox_bar.stop_confirm'),
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await updateStatus.mutateAsync({
        wsId: primary.workspaceId,
        timeboxId: primary.timebox.id,
        status: 'completed',
      });
      toaster.show({ tone: 'success', message: t('active_timebox_bar.stop_success') });
    } catch {
      toaster.show({ tone: 'danger', message: t('timeboxes.transition.error') });
    }
  };

  return (
    <div className={styles.bar} role="status" aria-live="polite">
      <span className={styles.label}>{t('active_timebox_bar.label')}</span>
      <div className={styles.body}>
        <span className={styles.name}>{primary.timebox.name}</span>
        <span className={styles.workspace}>
          {t('active_timebox_bar.workspace', { workspace: workspaceName })}
        </span>
      </div>
      <div className={styles.actions}>
        <Link to="/workspaces/$id/timeboxes" params={{ id: primary.workspaceId }}>
          <Button type="button" size="sm" variant="default">
            {t('active_timebox_bar.open')}
          </Button>
        </Link>
        <Button
          type="button"
          size="sm"
          variant="primary"
          onClick={() => {
            void handleStop();
          }}
          disabled={updateStatus.isPending}
        >
          {t('active_timebox_bar.stop')}
        </Button>
      </div>
    </div>
  );
}

/**
 * Public bar component. Self-fetches workspaces (suspense) so the app
 * shell only has to render `<ActiveTimeboxBar />` without wiring data.
 * The outer shell wraps this in a Suspense boundary with a `null`
 * fallback so the slot is invisible until the first render.
 */
export default function ActiveTimeboxBar(): ReactElement | null {
  const { data: workspaces } = useWorkspacesQuery();
  if (!workspaces || workspaces.length === 0) return null;
  return <ActiveTimeboxBarInner workspaces={workspaces} />;
}

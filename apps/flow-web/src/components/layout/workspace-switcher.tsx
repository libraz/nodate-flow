/**
 * WorkspaceSwitcher — native `<select>` dropdown that switches the
 * active workspace.
 *
 * Rendered in two places:
 *   - the desktop topbar left slot (`top-bar.tsx`), and
 *   - the sidebar drawer header (`sidebar.tsx`) — which is the only
 *     reachable copy at mobile widths where the topbar left slot is
 *     collapsed.
 *
 * Navigation: switching a workspace navigates to `/workspaces/{id}`
 * unless the current route is a cross-workspace page (calendar /
 * today / inbox / settings / pages). Those routes carry no workspace
 * id in the path, so there is nothing to navigate to — the switch is
 * committed through `setActiveWorkspaceId`, which re-renders every
 * `useCurrentWorkspaceId` reader so downstream workspace-keyed queries
 * move with it.
 *
 * Shares `styles.workspaceSelect` with the topbar so the visual
 * treatment is consistent across the two mount points.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate, useRouterState } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../../features/workspaces/api';
import { setActiveWorkspaceId, useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import styles from './top-bar.module.css';

/** Pages where switching workspace should NOT navigate away. */
const STAY_ON_PAGE_PREFIXES = ['/calendar', '/today', '/inbox', '/settings', '/pages'];

export default function WorkspaceSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const routeWsId = useCurrentWorkspaceId() ?? '';
  // Auto-select when there is exactly one workspace.
  const currentId = routeWsId || (workspaces.length === 1 ? (workspaces[0]?.id ?? '') : '');

  return (
    <select
      aria-label={t('topbar.workspace.switcher')}
      className={cx(styles.workspaceSelect, 'nf-focus-ring')}
      value={currentId}
      onChange={(e) => {
        const id = e.target.value;
        if (!id) return;
        // On cross-workspace pages, stay on the current page.
        const stayOnPage = STAY_ON_PAGE_PREFIXES.some((p) => pathname.startsWith(p));
        if (stayOnPage) {
          setActiveWorkspaceId(id);
          // Queries keyed on the workspace id re-fetch on their own once
          // the new id reaches them. This covers the rest: anything on
          // screen that read the old workspace without keying on it is
          // now showing another workspace's data. Only mounted queries
          // refetch, so the cost is bounded by what is actually visible.
          void queryClient.invalidateQueries({ refetchType: 'active' });
        } else {
          void navigate({ to: '/workspaces/$id', params: { id } });
        }
      }}
    >
      <option value="" disabled>
        {t('topbar.workspace.none')}
      </option>
      {workspaces.map((w) => (
        <option key={w.id} value={w.id}>
          {w.name}
        </option>
      ))}
    </select>
  );
}

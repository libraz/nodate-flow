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
 * today / inbox / settings / pages), in which case the switcher keeps
 * the user on the same page and the new id propagates via
 * `useCurrentWorkspaceId` to downstream queries.
 *
 * Shares `styles.workspaceSelect` with the topbar so the visual
 * treatment is consistent across the two mount points.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { useNavigate, useRouterState } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../../features/workspaces/api';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import styles from './top-bar.module.css';

/** Pages where switching workspace should NOT navigate away. */
const STAY_ON_PAGE_PREFIXES = ['/calendar', '/today', '/inbox', '/settings', '/pages'];

export default function WorkspaceSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const navigate = useNavigate();
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
          // Just changing the select value is enough — the workspace
          // context propagates via useCurrentWorkspaceId and queries
          // will refetch. Navigate to the same page to force re-render.
          void navigate({ to: pathname as never });
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

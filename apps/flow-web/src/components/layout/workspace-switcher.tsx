/**
 * WorkspaceSwitcher — shows the active workspace and, when there is a
 * choice, switches it.
 *
 *   - 0 workspaces: renders nothing.
 *   - 1 workspace: a static identity link (monogram + name) to that
 *     workspace; a choice of one is context, not a control.
 *   - 2+ workspaces: a native `<select>` dropdown.
 *
 * Rendered in two places, only one of which is visible at any width:
 *   - the topbar left slot (`top-bar.tsx`), and
 *   - the sidebar header (`sidebar.tsx`) — shown only while the topbar
 *     copy is hidden (narrow header, e.g. the mobile drawer).
 *
 * Navigation: switching a workspace navigates to `/workspaces/{id}`
 * unless the current route is a cross-workspace page (calendar /
 * today / inbox / settings / pages). Those routes carry no workspace
 * id in the path, so there is nothing to navigate to — the switch is
 * committed through `setActiveWorkspaceId`, which re-renders every
 * `useCurrentWorkspaceId` reader so downstream workspace-keyed queries
 * move with it.
 *
 * Built on the shared `Select` primitive; `styles.workspaceSelect` only
 * constrains its width per mount point.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Select from '@nodate-flow/ui/primitives/select';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useRouterState } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../../features/workspaces/api';
import { setActiveWorkspaceId, useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import styles from './top-bar.module.css';

/** Pages where switching workspace should NOT navigate away. */
const STAY_ON_PAGE_PREFIXES = ['/calendar', '/today', '/inbox', '/settings', '/pages'];

export default function WorkspaceSwitcher(): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const routeWsId = useCurrentWorkspaceId() ?? '';
  const currentId = routeWsId;

  if (workspaces.length === 0) return null;

  const only = workspaces.length === 1 ? workspaces[0] : undefined;
  if (only) {
    const label = t('topbar.workspace.current', { name: only.name });
    const monogram = (Array.from(only.name.trim())[0] ?? '').toUpperCase();
    return (
      <Link
        to="/workspaces/$id"
        params={{ id: only.id }}
        className={cx(styles.workspaceCurrent, 'nf-focus-ring')}
        aria-label={label}
        title={label}
      >
        <span className={styles.workspaceMonogram} aria-hidden="true">
          {monogram}
        </span>
        <span className={styles.workspaceCurrentName}>{only.name}</span>
      </Link>
    );
  }

  return (
    <Select
      aria-label={t('topbar.workspace.switcher')}
      className={styles.workspaceSelect}
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
    </Select>
  );
}

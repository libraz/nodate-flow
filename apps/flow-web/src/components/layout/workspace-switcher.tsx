/**
 * WorkspaceSwitcher — the one place a workspace is chosen, rendered at the
 * top of the sidebar. The shape is the same for one workspace or many: a
 * button naming the current workspace that opens a menu of workspaces, the
 * "all workspaces" destination and "new workspace". With no workspace it
 * renders nothing; the sidebar shows a create call-to-action instead.
 *
 * Choosing a workspace on a cross-workspace page (`/`, calendar, today,
 * inbox, settings, pages) carries no workspace id in the path, so the choice
 * is committed through `setActiveWorkspaceId` and the mounted queries are
 * refetched. Anywhere else it navigates to that workspace's project list.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Popover from '@nodate-flow/ui/primitives/popover';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useRouterState } from '@tanstack/react-router';
import { Check, ChevronsUpDown, Plus } from 'lucide-react';
import {
  type ButtonHTMLAttributes,
  type ReactElement,
  type ReactNode,
  type Ref,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../../features/workspaces/api';
import WorkspaceCreateDialog from '../../features/workspaces/workspace-create-dialog';
import { setActiveWorkspaceId, useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { navEntry } from './nav-model';
import styles from './sidebar.module.css';

/** Pages where switching workspace should NOT navigate away. */
const STAY_ON_PAGE_PREFIXES = ['/calendar', '/today', '/inbox', '/settings', '/pages'];

const ALL_WORKSPACES = navEntry('allWorkspaces');

/** First letter of a workspace name, for the monogram. */
function monogramOf(name: string): string {
  return (Array.from(name.trim())[0] ?? '').toUpperCase();
}

type TriggerProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  ref?: Ref<HTMLElement>;
  label: string;
  tooltip: string | null;
  expanded: boolean;
  children: ReactNode;
};

/**
 * The popover clones its child with a ref and interaction props, and the
 * tooltip needs the button's own ref, so the popover anchors on a span.
 */
function SwitcherTrigger({
  ref,
  label,
  tooltip,
  expanded,
  children,
  ...buttonProps
}: TriggerProps): ReactElement {
  const button = (
    <button
      type="button"
      className={cx(styles.switcherButton, 'nf-focus-ring')}
      aria-label={label}
      {...buttonProps}
      aria-haspopup="menu"
      aria-expanded={expanded}
    >
      {children}
    </button>
  );
  return (
    <span ref={ref} className={styles.switcherAnchor}>
      {tooltip === null ? (
        button
      ) : (
        <Tooltip content={tooltip} placement="right">
          {button}
        </Tooltip>
      )}
    </span>
  );
}

export default function WorkspaceSwitcher({
  railCollapsed = false,
}: {
  railCollapsed?: boolean;
}): ReactElement | null {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const currentId = useCurrentWorkspaceId();
  const [menuOpen, setMenuOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  if (workspaces.length === 0) return null;

  // Until the member list resolves there is no current id; the hook then
  // auto-selects the first workspace, so show that one in the meantime.
  const current = workspaces.find((w) => w.id === currentId) ?? workspaces[0];
  const currentName = current?.name ?? t('topbar.workspace.none');

  const handleSelect = (id: string): void => {
    setMenuOpen(false);
    const stayOnPage =
      pathname === '/' || STAY_ON_PAGE_PREFIXES.some((p) => pathname.startsWith(p));
    if (stayOnPage) {
      setActiveWorkspaceId(id);
      // Queries keyed on the workspace id refetch on their own; this covers
      // anything on screen that read the old workspace without keying on it.
      void queryClient.invalidateQueries({ refetchType: 'active' });
    } else {
      void navigate({ to: '/workspaces/$id/projects', params: { id } });
    }
  };

  const menu = (
    <div className={styles.switcherMenu}>
      {/* biome-ignore lint/a11y/noNoninteractiveElementToInteractiveRole: deliberate ARIA menu — the ul / li role="presentation" / button role="menuitemradio" structure is the standard menu pattern. */}
      <ul className={styles.switcherList} role="menu" aria-label={t('nav.switchWorkspace')}>
        {workspaces.map((w) => (
          <li key={w.id} role="presentation">
            <button
              type="button"
              role="menuitemradio"
              aria-checked={w.id === current?.id}
              className={cx(styles.switcherItem, 'nf-focus-ring-inset')}
              onClick={() => {
                handleSelect(w.id);
              }}
            >
              <span
                className={styles.monogram}
                data-monogram={monogramOf(w.name)}
                aria-hidden="true"
              />
              <span className={styles.switcherItemName}>{w.name}</span>
              {w.id === current?.id ? <Icon icon={Check} decorative /> : null}
            </button>
          </li>
        ))}
        <li aria-hidden="true" className={styles.switcherDivider} />
        <li role="presentation">
          <Link
            to={ALL_WORKSPACES.route}
            role="menuitem"
            className={cx(styles.switcherItem, 'nf-focus-ring-inset')}
            onClick={() => {
              setMenuOpen(false);
            }}
          >
            <Icon icon={ALL_WORKSPACES.icon} decorative />
            <span className={styles.switcherItemName}>{t(ALL_WORKSPACES.labelKey)}</span>
          </Link>
        </li>
        <li role="presentation">
          <button
            type="button"
            role="menuitem"
            className={cx(styles.switcherItem, 'nf-focus-ring-inset')}
            onClick={() => {
              setMenuOpen(false);
              setCreateOpen(true);
            }}
          >
            <Icon icon={Plus} decorative />
            <span className={styles.switcherItemName}>{t('nav.newWorkspace')}</span>
          </button>
        </li>
      </ul>
    </div>
  );

  return (
    <>
      <Popover
        open={menuOpen}
        onOpenChange={setMenuOpen}
        placement="bottom-start"
        content={menu}
        ariaLabel={t('nav.switchWorkspace')}
        className={styles.switcherPopover}
      >
        <SwitcherTrigger
          label={`${currentName}: ${t('nav.switchWorkspace')}`}
          tooltip={railCollapsed ? currentName : null}
          expanded={menuOpen}
        >
          <span
            className={styles.monogram}
            data-monogram={monogramOf(currentName)}
            aria-hidden="true"
          />
          {railCollapsed ? null : (
            <>
              <span className={styles.switcherName}>{currentName}</span>
              <Icon icon={ChevronsUpDown} decorative />
            </>
          )}
        </SwitcherTrigger>
      </Popover>
      <WorkspaceCreateDialog
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </>
  );
}

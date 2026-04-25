/**
 * CalendarsRail — right-rail panel for `/calendar` that lists every
 * calendar the actor is subscribed to in every workspace they belong
 * to, grouped per workspace.
 *
 * Each row offers two controls:
 *   - An eye-icon visibility toggle that PATCHes the actor's own
 *     subscription (`visible`) — flipping the bit hides events of
 *     that calendar from the cross-workspace month grid without
 *     unsubscribing.
 *   - A more-menu (3-dot popover) with "Hide / Show" (mirrors the
 *     eye) and "Leave calendar" (DELETE the actor's own membership).
 *     The leave item is gated to non-personal calendars so the user
 *     cannot accidentally drop their own personal calendar.
 *
 * Multi-workspace support is intentional: the route renders calendars
 * across every workspace the user belongs to. When the actor only
 * has a single workspace the per-section header is suppressed so the
 * rail reads as a flat list.
 *
 * The component does not own the drawer state — it emits an
 * `onAddTeammate(workspaceId)` callback so the surrounding route can
 * mount {@link AddTeammateDrawer} and pass the active workspace
 * through.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import { useQueries } from '@tanstack/react-query';
import { Eye, EyeOff, MoreVertical } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { timeSdk } from '../../lib/sdk';
import { type RailCalendar, usePatchOwnSubscriptionMutation, useUnsubscribeMutation } from './api';
import styles from './calendars-rail.module.css';

interface CalendarsRailProps {
  /**
   * Workspaces the actor belongs to. Pass through `useWorkspacesQuery`
   * data — the rail only needs `id` and `name` so the prop is kept
   * narrow to avoid coupling to the full workspace schema.
   */
  workspaces: { id: string; name: string }[];
  /**
   * Actor's own user public id, used as the `userId` path segment when
   * leaving a calendar via `members-remove`.
   */
  selfUserId: string;
  /**
   * Invoked when the user clicks "Add teammate calendar..." in any
   * workspace section. The route opens the drawer scoped to `wsId`.
   */
  onAddTeammate: (wsId: string) => void;
}

/**
 * Top-level rail. Fans calendar queries across `workspaces` so a
 * single render covers every section. Each section renders even
 * while the per-workspace query is pending so the layout doesn't
 * jitter.
 */
export default function CalendarsRail({
  workspaces,
  selfUserId,
  onAddTeammate,
}: CalendarsRailProps): ReactElement {
  const { t } = useTranslation('common');

  // Fan out per-workspace queries the same way the route already does
  // for projects. The query key matches the calendar-events feature's
  // `useCalendarsQuery` so both surfaces share the same cache row.
  const calendarQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['calendar-events', 'calendars', w.id] as const,
      staleTime: 60_000,
      queryFn: async (): Promise<RailCalendar[]> => {
        const { data, error } = await timeSdk.GET('/workspaces/{wsId}/calendars', {
          params: { path: { wsId: w.id } },
        });
        if (error || !data) return [];
        return data.calendars ?? [];
      },
    })),
  });

  const showHeaders = workspaces.length > 1;

  return (
    <aside aria-label={t('calendars_rail.title')} className={styles.rail}>
      <h2 className={styles.title}>{t('calendars_rail.title')}</h2>
      {workspaces.map((ws, idx) => {
        const calendars = calendarQueries[idx]?.data ?? [];
        return (
          <CalendarsSection
            key={ws.id}
            workspace={ws}
            calendars={calendars}
            selfUserId={selfUserId}
            showHeader={showHeaders}
            onAddTeammate={onAddTeammate}
          />
        );
      })}
    </aside>
  );
}

interface CalendarsSectionProps {
  workspace: { id: string; name: string };
  calendars: RailCalendar[];
  selfUserId: string;
  showHeader: boolean;
  onAddTeammate: (wsId: string) => void;
}

/**
 * Single workspace section: optional header, calendar rows, and the
 * "Add teammate calendar..." trigger.
 */
function CalendarsSection({
  workspace,
  calendars,
  selfUserId,
  showHeader,
  onAddTeammate,
}: CalendarsSectionProps): ReactElement {
  const { t } = useTranslation('common');
  const sortedCalendars = [...calendars].sort(
    (a, b) => a.subscriptionSortWeight - b.subscriptionSortWeight,
  );

  return (
    <section className={styles.section}>
      {showHeader ? <h3 className={styles.sectionHeader}>{workspace.name}</h3> : null}
      {sortedCalendars.length === 0 ? (
        <p className={styles.empty}>{t('calendars_rail.discover.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {sortedCalendars.map((cal) => (
            <CalendarRow
              key={cal.id}
              workspaceId={workspace.id}
              calendar={cal}
              selfUserId={selfUserId}
            />
          ))}
        </ul>
      )}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className={styles.addButton}
        onClick={() => onAddTeammate(workspace.id)}
      >
        {t('calendars_rail.add_teammate')}
      </Button>
    </section>
  );
}

interface CalendarRowProps {
  workspaceId: string;
  calendar: RailCalendar;
  selfUserId: string;
}

/**
 * Single calendar row with eye toggle + 3-dot popover menu. Both
 * controls funnel through the actor's own subscription patch /
 * delete endpoints; calendar metadata (name, color) is read-only
 * from this surface.
 */
function CalendarRow({ workspaceId, calendar, selfUserId }: CalendarRowProps): ReactElement {
  const { t } = useTranslation('common');
  const patchSub = usePatchOwnSubscriptionMutation();
  const leaveCal = useUnsubscribeMutation();
  const [menuOpen, setMenuOpen] = useState(false);

  const isPersonal = calendar.kind === 'personal';
  const pending = patchSub.isPending || leaveCal.isPending;
  const Icon = calendar.visible ? Eye : EyeOff;

  const handleToggleVisibility = (): void => {
    patchSub.mutate({
      wsId: workspaceId,
      calId: calendar.id,
      body: { visible: !calendar.visible },
    });
  };

  const handleLeave = (): void => {
    setMenuOpen(false);
    leaveCal.mutate({ wsId: workspaceId, calId: calendar.id, userId: selfUserId });
  };

  const menuContent = (
    <ul className={styles.menu} role="menu">
      <li>
        <button
          type="button"
          role="menuitem"
          className={styles.menuItem}
          onClick={() => {
            setMenuOpen(false);
            handleToggleVisibility();
          }}
          disabled={pending}
        >
          {calendar.visible ? t('calendars_rail.actions.hide') : t('calendars_rail.actions.show')}
        </button>
      </li>
      {isPersonal ? null : (
        <li>
          <button
            type="button"
            role="menuitem"
            className={`${styles.menuItem} ${styles['menuItem--danger']}`}
            onClick={handleLeave}
            disabled={pending}
          >
            {t('calendars_rail.actions.leave')}
          </button>
        </li>
      )}
    </ul>
  );

  return (
    <li className={`${styles.row}${calendar.visible ? '' : ` ${styles['row--hidden']}`}`}>
      <span aria-hidden className={styles.dot} style={{ background: calendar.displayColor }} />
      <span className={styles.name} title={calendar.name}>
        {calendar.name}
      </span>
      <span className={styles.actions}>
        <button
          type="button"
          className={styles.iconButton}
          onClick={handleToggleVisibility}
          disabled={pending}
          aria-label={t('calendars_rail.actions.toggle_visibility')}
          aria-pressed={calendar.visible}
        >
          <Icon size={14} aria-hidden />
        </button>
        <Popover
          placement="bottom-end"
          open={menuOpen}
          onOpenChange={setMenuOpen}
          content={menuContent}
        >
          <button
            type="button"
            className={styles.iconButton}
            aria-label={t('calendars_rail.actions.menu')}
            aria-haspopup="menu"
            aria-expanded={menuOpen}
          >
            <MoreVertical size={14} aria-hidden />
          </button>
        </Popover>
      </span>
    </li>
  );
}

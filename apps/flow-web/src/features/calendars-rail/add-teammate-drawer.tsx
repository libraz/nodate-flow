/**
 * AddTeammateDrawer — side-sheet that lists teammate personal
 * calendars the actor can subscribe to in the active workspace and
 * exposes a one-click "Subscribe" button per row.
 *
 * Backed by `GET /workspaces/{wsId}/discoverable-calendars`. Subscribe
 * goes through the rail feature's `useSubscribeToCalendarMutation`
 * which invalidates both the discoverable list (so the subscribed
 * row vanishes) and the workspace's subscribed calendar list (so the
 * rail picks up the new entry).
 *
 * The drawer does not auto-close on success — the user often
 * subscribes to several teammates in one go. Closing is left to the
 * dialog primitive's escape / overlay-click handlers and the
 * caller's explicit close trigger.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError } from '../../lib/api-error';
import styles from './add-teammate-drawer.module.css';
import {
  type DiscoverableCalendar,
  useDiscoverableCalendarsQuery,
  useSubscribeToCalendarMutation,
} from './api';

interface AddTeammateDrawerProps {
  open: boolean;
  workspaceId: string;
  onClose: () => void;
}

/**
 * Returns up to 2 capitalised initials from a display name. Used as
 * the avatar fallback when the calendar owner has no avatar URL.
 */
function initialsFor(displayName: string): string {
  const parts = displayName.trim().split(/\s+/u).filter(Boolean);
  if (parts.length === 0) return '?';
  if (parts.length === 1) {
    const word = parts[0];
    return word ? word.slice(0, 2).toUpperCase() : '?';
  }
  const first = parts[0]?.charAt(0) ?? '';
  const last = parts[parts.length - 1]?.charAt(0) ?? '';
  return `${first}${last}`.toUpperCase();
}

export default function AddTeammateDrawer({
  open,
  workspaceId,
  onClose,
}: AddTeammateDrawerProps): ReactElement {
  const { t } = useTranslation('common');

  // Only fire the query while the drawer is open so opening doesn't
  // pre-warm a list the user might never reach. The query key still
  // keys on `workspaceId` so re-opening across switches refetches.
  const { data, isLoading, error } = useDiscoverableCalendarsQuery(open ? workspaceId : null);
  const subscribe = useSubscribeToCalendarMutation();

  const [search, setSearch] = useState('');

  const filtered = useMemo<DiscoverableCalendar[]>(() => {
    const list = data ?? [];
    const q = search.trim().toLowerCase();
    if (q.length === 0) return list;
    return list.filter((row) => {
      return row.ownerDisplayName.toLowerCase().includes(q) || row.name.toLowerCase().includes(q);
    });
  }, [data, search]);

  const handleSearchChange = (event: ChangeEvent<HTMLInputElement>): void => {
    setSearch(event.target.value);
  };

  const handleSubscribe = (calId: string): void => {
    subscribe.mutate(
      { wsId: workspaceId, calId },
      {
        onSuccess: () => {
          toaster.show({
            tone: 'success',
            message: t('calendars_rail.discover.subscribed_toast'),
          });
        },
        onError: (err) => {
          const message =
            err instanceof ApiError ? err.message : t('calendars_rail.discover.empty');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t('calendars_rail.discover.title')}
      side="inline-end"
    >
      <div className={styles.body}>
        <Input
          type="search"
          className={styles.search}
          placeholder={t('calendars_rail.discover.search_placeholder')}
          value={search}
          onChange={handleSearchChange}
          aria-label={t('calendars_rail.discover.search_placeholder')}
        />

        {isLoading ? (
          <div className={styles.body} aria-busy="true">
            <Skeleton style={{ blockSize: '3rem' }} />
            <Skeleton style={{ blockSize: '3rem' }} />
            <Skeleton style={{ blockSize: '3rem' }} />
          </div>
        ) : error ? (
          <p className={styles.error} role="alert">
            {error instanceof ApiError ? error.message : String(error)}
          </p>
        ) : filtered.length === 0 ? (
          <p className={styles.empty}>{t('calendars_rail.discover.empty')}</p>
        ) : (
          <ul className={styles.list}>
            {filtered.map((row) => (
              <li key={row.id} className={styles.row}>
                <Avatar
                  {...(row.ownerAvatarUrl ? { src: row.ownerAvatarUrl } : {})}
                  alt={row.ownerDisplayName}
                  initials={initialsFor(row.ownerDisplayName)}
                  size="md"
                />
                <div className={styles.identity}>
                  <span className={styles.displayName} title={row.ownerDisplayName}>
                    {row.ownerDisplayName}
                  </span>
                  <span className={styles.calendarMeta}>
                    <span aria-hidden className={styles.dot} style={{ background: row.color }} />
                    <span className={styles.calendarName} title={row.name}>
                      {row.name}
                    </span>
                  </span>
                </div>
                <Button
                  type="button"
                  variant="primary"
                  size="sm"
                  onClick={() => handleSubscribe(row.id)}
                  disabled={subscribe.isPending}
                >
                  {t('calendars_rail.discover.subscribe')}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Drawer>
  );
}

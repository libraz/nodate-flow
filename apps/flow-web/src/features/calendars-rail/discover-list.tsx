/**
 * DiscoverList — inline body that lists teammate personal calendars
 * the actor can subscribe to in the active workspace and exposes a
 * one-click "Subscribe" button per row.
 *
 * Rendered inside {@link CalendarsRail} when its section is in
 * "discover" mode (the rail morphs into a discovery view rather than
 * pushing a side-drawer). Visually the list reads as a continuation
 * of the rail: same column width, same token-driven row chrome.
 *
 * Backed by `GET /workspaces/{wsId}/discoverable-calendars`.
 * Subscribe goes through {@link useSubscribeToCalendarMutation} which
 * invalidates both the discoverable list (so the subscribed row
 * vanishes) and the workspace's subscribed calendar list (so the rail
 * picks up the new entry).
 *
 * The list does not auto-leave discover mode on success — users
 * commonly subscribe to several teammates in one go. The caller is
 * responsible for offering a back affordance via {@link onClose}; the
 * rail wires that to its header back button.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type KeyboardEvent, type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import {
  type DiscoverableCalendar,
  useDiscoverableCalendarsQuery,
  useSubscribeToCalendarMutation,
} from './api';
import styles from './calendars-rail.module.css';

interface DiscoverListProps {
  /** Workspace whose discoverable calendars are being listed. */
  workspaceId: string;
  /**
   * Invoked when the user dismisses the discovery view from inside
   * the list — currently bound to the search input's Escape key. The
   * parent section also renders its own back-arrow button that
   * triggers the same transition; this prop lets the list participate
   * in keyboard dismissal without reaching up into the header.
   */
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

export default function DiscoverList({ workspaceId, onClose }: DiscoverListProps): ReactElement {
  const { t } = useTranslation('common');

  const { data, isLoading, error } = useDiscoverableCalendarsQuery(workspaceId);
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

  /**
   * Escape from the search input dismisses the discovery view. The
   * field clears its own value first if there is one, otherwise the
   * keystroke bubbles up to {@link onClose} so the rail returns to
   * list mode — same pattern as the combobox primitive.
   */
  const handleSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>): void => {
    if (event.key !== 'Escape') return;
    if (search.length > 0) {
      event.preventDefault();
      setSearch('');
      return;
    }
    event.preventDefault();
    onClose();
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
          const message = formatApiError(err, t, 'calendars_rail.discover.subscribe_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <div className={styles.discoverBody}>
      <Input
        type="search"
        className={styles.discoverSearch}
        placeholder={t('calendars_rail.discover.search_placeholder')}
        value={search}
        onChange={handleSearchChange}
        onKeyDown={handleSearchKeyDown}
        aria-label={t('calendars_rail.discover.search_placeholder')}
      />

      {isLoading ? (
        <div className={styles.discoverSkeletons} aria-busy="true">
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '3rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '3rem' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '3rem' }} />
        </div>
      ) : error ? (
        <p className={styles.discoverError} role="alert">
          {formatApiError(error, t, 'calendars_rail.discover.load_error')}
        </p>
      ) : filtered.length === 0 ? (
        <p className={styles.empty}>{t('calendars_rail.discover.empty')}</p>
      ) : (
        <ul className={styles.discoverList}>
          {filtered.map((row) => (
            <li key={row.id} className={styles.discoverRow}>
              <Avatar
                {...(row.ownerAvatarUrl ? { src: row.ownerAvatarUrl } : {})}
                alt={row.ownerDisplayName}
                initials={initialsFor(row.ownerDisplayName)}
                size="md"
              />
              <div className={styles.discoverIdentity}>
                <span className={styles.discoverDisplayName} title={row.ownerDisplayName}>
                  {row.ownerDisplayName}
                </span>
                <span className={styles.discoverCalendarMeta}>
                  <span
                    aria-hidden
                    className={styles.discoverDot}
                    style={{ background: row.color }}
                  />
                  <span className={styles.discoverCalendarName} title={row.name}>
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
  );
}

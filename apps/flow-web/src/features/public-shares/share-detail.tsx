/**
 * ShareDetail — editor page for a single public share. Lists attached events,
 * lets the workspace admin detach individual events, and opens a picker to
 * add new ones from the workspace's calendar events.
 *
 * Data:
 *   - `GET /workspaces/{wsId}/public-shares/{shareId}` returns share metadata
 *     plus the already-attached events (editor projection).
 *   - `GET /workspaces/{wsId}/calendar-events?start=&end=` lists candidates for
 *     the picker; confidential events are filtered client-side and also
 *     rejected server-side at attach time.
 */

import {
  closestCenter,
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import { usePresence } from '@nodate-flow/ui/hooks/use-presence';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Link } from '@tanstack/react-router';
import { ArrowLeft, GripVertical, Plus } from 'lucide-react';
import { type CSSProperties, type ReactElement, Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import AddEventsDialog from './add-events-dialog';
import {
  type ShareEvent,
  type ShareOverrideWarning,
  useAttachEventsToShare,
  useDetachEventFromShare,
  usePublicShareDetailQuery,
  useReorderShareEvents,
} from './api';
import styles from './share-detail.module.css';
import ShareOverrideNotice from './share-override-notice';

/** Columns in the attached-events table, so a notice can span the row. */
const TABLE_COLUMNS = 5;

export interface ShareDetailProps {
  workspaceId: string;
  shareId: string;
}

/** Format a start/end pair into a compact localised range label. */
function formatWhen(event: ShareEvent, locale: string, allDayLabel: string): string {
  if (!event.startAt) return '—';
  const start = new Date(event.startAt * 1000);
  const end = event.endAt ? new Date(event.endAt * 1000) : null;
  const dateFmt = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
  const timeFmt = new Intl.DateTimeFormat(locale, { timeStyle: 'short' });
  if (event.allDay) {
    if (!end || sameDay(start, end)) return `${dateFmt.format(start)} · ${allDayLabel}`;
    return `${dateFmt.format(start)} – ${dateFmt.format(end)}`;
  }
  if (!end) return `${dateFmt.format(start)} ${timeFmt.format(start)}`;
  if (sameDay(start, end)) {
    return `${dateFmt.format(start)} ${timeFmt.format(start)}–${timeFmt.format(end)}`;
  }
  return `${dateFmt.format(start)} ${timeFmt.format(start)} – ${dateFmt.format(end)} ${timeFmt.format(end)}`;
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

export default function ShareDetail({ workspaceId, shareId }: ShareDetailProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data } = usePublicShareDetailQuery(workspaceId, shareId);
  const detach = useDetachEventFromShare(workspaceId, shareId);
  const reorder = useReorderShareEvents(workspaceId, shareId);
  const attach = useAttachEventsToShare(workspaceId, shareId);
  const [pickerOpen, setPickerOpen] = useState(false);

  // Local, optimistic ordering mirrors the server list but is mutated
  // immediately on drag end so the UI does not flicker while the
  // mutation is in flight. Re-synced whenever the server list changes
  // (including the invalidation fired from the reorder mutation).
  const serverEvents: ShareEvent[] = data.events ?? [];
  const serverOrderKey = serverEvents.map((e) => e.linkId).join(',');
  const [orderedEvents, setOrderedEvents] = useState<ShareEvent[]>(serverEvents);
  // The dependency on the stringified id order is intentional: we only
  // want to reset when the server's identity/order of rows changes, not
  // on every query object identity.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-sync only when server link order changes
  useEffect(() => {
    setOrderedEvents(serverEvents);
  }, [serverOrderKey]);

  const { entries, onExited } = usePresenceList(orderedEvents, (e) => e.linkId);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const reordering = reorder.isPending;

  const handleDragEnd = (event: DragEndEvent): void => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const oldIndex = orderedEvents.findIndex((e) => e.linkId === active.id);
    const newIndex = orderedEvents.findIndex((e) => e.linkId === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const next = arrayMove(orderedEvents, oldIndex, newIndex);
    setOrderedEvents(next);
    reorder.mutate(
      next.map((e) => e.linkId),
      {
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'workspace.public_shares.detail.errors.reorder_failed'),
          });
        },
      },
    );
  };

  const dragAnnouncements = {
    onDragStart({ active }: { active: { id: string | number } }): string {
      return t('workspace.public_shares.detail.reorder_a11y.lifted', {
        id: String(active.id),
      });
    },
    onDragOver({
      active,
      over,
    }: {
      active: { id: string | number };
      over: { id: string | number } | null;
    }): string {
      if (!over) return '';
      return t('workspace.public_shares.detail.reorder_a11y.over', {
        active: String(active.id),
        over: String(over.id),
      });
    },
    onDragEnd({
      active,
      over,
    }: {
      active: { id: string | number };
      over: { id: string | number } | null;
    }): string {
      if (!over) {
        return t('workspace.public_shares.detail.reorder_a11y.cancelled', {
          id: String(active.id),
        });
      }
      return t('workspace.public_shares.detail.reorder_a11y.dropped', {
        active: String(active.id),
        over: String(over.id),
      });
    },
    onDragCancel({ active }: { active: { id: string | number } }): string {
      return t('workspace.public_shares.detail.reorder_a11y.cancelled', {
        id: String(active.id),
      });
    },
  };

  // Detaching is undone by attaching the event again, so it acts at once.
  const handleDetach = async (event: ShareEvent): Promise<void> => {
    try {
      await detach.mutateAsync(event.eventId);
      toaster.show({
        tone: 'info',
        message: t('workspace.public_shares.detail.detached'),
        action: {
          label: t('common:common.undo'),
          onAction: () => {
            attach.mutate([event.eventId], {
              onError: (err) => {
                toaster.show({
                  tone: 'danger',
                  message: formatApiError(
                    err,
                    t,
                    'workspace.public_shares.detail.errors.attach_failed',
                  ),
                });
              },
            });
          },
        },
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.public_shares.detail.errors.detach_failed'),
      });
    }
  };

  /**
   * Publish the row that replaces a moved occurrence. The server is still
   * the authority on whether it may be published, so a response that
   * attached nothing is reported as a failure rather than announced as a
   * fix — the page would otherwise keep showing the old time under a
   * success message.
   */
  const handleAttachOverride = async (warning: ShareOverrideWarning): Promise<void> => {
    try {
      const result = await attach.mutateAsync([warning.eventId]);
      if (result.attached < 1) {
        toaster.show({
          tone: 'danger',
          message: t('workspace.public_shares.detail.errors.attach_failed'),
        });
        return;
      }
      toaster.show({
        tone: 'success',
        message: t('workspace.public_shares.detail.override_notice.attached'),
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.public_shares.detail.errors.attach_failed'),
      });
    }
  };

  const allDayLabel = t('workspace.public_shares.detail.event_all_day');
  const attachedIds = new Set(orderedEvents.map((e) => e.eventId));
  const dragHandleLabel = t('workspace.public_shares.detail.reorder_handle_label');

  return (
    <section className={styles.section}>
      <div>
        <Link
          to="/workspaces/$id/settings/public-shares"
          params={{ id: workspaceId }}
          className={styles.backLink}
        >
          <ArrowLeft size={14} aria-hidden />
          {t('workspace.public_shares.detail.back')}
        </Link>
      </div>

      <header className={styles.header}>
        <div className={styles.headerIdentity}>
          <h1 className={styles.title}>{data.share.title}</h1>
          {data.share.description ? (
            <p className={styles.headerDescription}>{data.share.description}</p>
          ) : null}
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setPickerOpen(true);
          }}
        >
          <Plus size={14} aria-hidden className={styles.addIcon} />
          {t('workspace.public_shares.detail.add_events')}
        </Button>
      </header>

      <div>
        <h2 className={styles.eventsHeading}>{t('workspace.public_shares.detail.events_title')}</h2>
        <p className={styles.eventsDescription}>
          {t('workspace.public_shares.detail.events_description')}
        </p>
      </div>

      {entries.length > 0 ? (
        <div
          className={`${styles.tableWrap} ${reordering ? styles.tableWrapBusy : ''}`.trim()}
          aria-busy={reordering || undefined}
        >
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragEnd={handleDragEnd}
            accessibility={{ announcements: dragAnnouncements }}
          >
            <table className={styles.table}>
              <thead>
                <tr className={styles.headerRow}>
                  {/* The label is carried as hidden text rather than an
                      aria-label: a header cell with no text content is
                      reported as an empty header, and screen readers read
                      the text either way. */}
                  <th scope="col" className={styles.handleHeaderCell}>
                    <VisuallyHidden>{dragHandleLabel}</VisuallyHidden>
                  </th>
                  <th scope="col" className={styles.headerCell}>
                    {t('workspace.public_shares.detail.table.event')}
                  </th>
                  <th scope="col" className={styles.headerCell}>
                    {t('workspace.public_shares.detail.table.when')}
                  </th>
                  <th scope="col" className={styles.headerCell}>
                    {t('workspace.public_shares.detail.table.calendar')}
                  </th>
                  <th scope="col" className={styles.headerCellEnd}>
                    {t('workspace.public_shares.detail.table.actions')}
                  </th>
                </tr>
              </thead>
              <SortableContext
                items={orderedEvents.map((e) => e.linkId)}
                strategy={verticalListSortingStrategy}
              >
                <tbody>
                  {entries.map(({ key, item: event, present }) => (
                    <SortableRow
                      key={key}
                      event={event}
                      present={present}
                      onExited={() => onExited(key)}
                      locale={locale}
                      allDayLabel={allDayLabel}
                      undatedLabel={t('workspace.public_shares.detail.event_undated')}
                      detachLabel={t('workspace.public_shares.detail.detach')}
                      handleLabel={dragHandleLabel}
                      disabled={reordering}
                      attaching={attach.isPending}
                      detaching={detach.isPending}
                      onDetach={handleDetach}
                      onAttachOverride={handleAttachOverride}
                    />
                  ))}
                </tbody>
              </SortableContext>
            </table>
          </DndContext>
        </div>
      ) : (
        <p className={styles.empty}>{t('workspace.public_shares.detail.empty_events')}</p>
      )}

      <Suspense
        fallback={
          <div className={styles.suspenseFallback}>
            <Skeleton className={styles.suspenseSkeleton} />
          </div>
        }
      >
        <AddEventsDialog
          workspaceId={workspaceId}
          shareId={shareId}
          open={pickerOpen}
          attachedIds={attachedIds}
          onClose={() => {
            setPickerOpen(false);
          }}
        />
      </Suspense>
    </section>
  );
}

interface SortableRowProps {
  event: ShareEvent;
  /** False while the row plays its exit after the event left the list. */
  present: boolean;
  onExited: () => void;
  locale: string;
  allDayLabel: string;
  undatedLabel: string;
  detachLabel: string;
  handleLabel: string;
  disabled: boolean;
  attaching: boolean;
  detaching: boolean;
  onDetach: (event: ShareEvent) => Promise<void>;
  onAttachOverride: (warning: ShareOverrideWarning) => Promise<void>;
}

/**
 * One draggable row in the attached-events table. The drag handle is a dedicated
 * button in the first cell so the rest of the row (especially the Remove action)
 * stays clickable without triggering drags. Keyboard users focus the handle and
 * use Space + arrow keys via `@dnd-kit`'s KeyboardSensor.
 *
 * A series the share still shows at a start it moved away from carries a
 * second row underneath it. Only the sortable row is a drag item; the notice
 * repeats its transform so the two travel together.
 */
function SortableRow({
  event,
  present,
  onExited,
  locale,
  allDayLabel,
  undatedLabel,
  detachLabel,
  handleLabel,
  disabled,
  attaching,
  detaching,
  onDetach,
  onAttachOverride,
}: SortableRowProps): ReactElement | null {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: event.linkId,
    disabled: disabled || !present,
  });
  const presence = usePresence<HTMLTableRowElement>(present);
  // biome-ignore lint/correctness/useExhaustiveDependencies: fires once per completed exit; onExited is recreated every render
  useEffect(() => {
    if (!presence.mounted) onExited();
  }, [presence.mounted]);
  const setRowRef = (node: HTMLTableRowElement | null): void => {
    setNodeRef(node);
    presence.ref(node);
  };

  // Inline style carries only the dnd-kit-driven transform / transition;
  // the drag highlight lives in the CSS module.
  const rowStyle: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  // `unpublishedOverrides` is absent on a series with nothing wrong, so the
  // notice is driven by the list being non-empty rather than by the field
  // being present.
  const warnings = event.unpublishedOverrides ?? [];

  if (!presence.mounted) return null;

  return (
    <>
      <tr
        ref={setRowRef}
        data-state={presence.state}
        inert={!present}
        className={`${styles.row}${isDragging ? ` ${styles['row--dragging']}` : ''}${present ? '' : ` ${motion.exitRow}`}`}
        style={rowStyle}
      >
        <td className={styles.handleCell}>
          <button
            type="button"
            aria-label={handleLabel}
            disabled={disabled}
            {...attributes}
            {...listeners}
            className={styles.dragHandle}
          >
            <GripVertical size={14} aria-hidden />
          </button>
        </td>
        <td className={styles.cell}>
          <div className={styles.eventTitle}>{event.title}</div>
          {event.location ? <div className={styles.eventLocation}>{event.location}</div> : null}
        </td>
        <td className={styles.cell}>
          {event.startAt ? formatWhen(event, locale, allDayLabel) : undatedLabel}
        </td>
        <td className={styles.cell}>{event.calendarName}</td>
        <td className={styles.cellActions}>
          <Button
            type="button"
            variant="ghost"
            disabled={detaching}
            onClick={() => {
              void onDetach(event);
            }}
          >
            {detachLabel}
          </Button>
        </td>
      </tr>
      {present && warnings.length > 0 ? (
        <ShareOverrideNotice
          warnings={warnings}
          allDay={event.allDay}
          locale={locale}
          columnCount={TABLE_COLUMNS}
          busy={attaching}
          rowStyle={rowStyle}
          onAttach={(warning) => {
            void onAttachOverride(warning);
          }}
        />
      ) : null}
    </>
  );
}

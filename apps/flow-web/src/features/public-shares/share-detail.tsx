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
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { ArrowLeft, GripVertical, Plus } from 'lucide-react';
import { type CSSProperties, type ReactElement, Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import AddEventsDialog from './add-events-dialog';
import {
  type ShareEvent,
  useDetachEventFromShare,
  usePublicShareDetailQuery,
  useReorderShareEvents,
} from './api';

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
        onError: () => {
          toaster.show({
            tone: 'danger',
            message: t('workspace.public_shares.detail.errors.reorder_failed'),
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

  const handleDetach = async (event: ShareEvent): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('workspace.public_shares.detail.detach_confirm', { title: event.title }),
      }))
    ) {
      return;
    }
    try {
      await detach.mutateAsync(event.eventId);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.detail.detached') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.public_shares.detail.errors.detach_failed'),
      });
    }
  };

  const allDayLabel = t('workspace.public_shares.detail.event_all_day');
  const attachedIds = new Set(orderedEvents.map((e) => e.eventId));
  const dragHandleLabel = t('workspace.public_shares.detail.reorder_handle_label');

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div>
        <Link
          to="/workspaces/$id/settings/public-shares"
          params={{ id: workspaceId }}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.25rem',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.8125rem',
            textDecoration: 'none',
          }}
        >
          <ArrowLeft size={14} aria-hidden />
          {t('workspace.public_shares.detail.back')}
        </Link>
      </div>

      <header
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '1rem',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{data.share.title}</h1>
          {data.share.description ? (
            <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
              {data.share.description}
            </p>
          ) : null}
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setPickerOpen(true);
          }}
        >
          <Plus size={14} aria-hidden style={{ marginInlineEnd: '0.25rem' }} />
          {t('workspace.public_shares.detail.add_events')}
        </Button>
      </header>

      <div>
        <h2 style={{ margin: '0 0 0.25rem', fontSize: '1rem' }}>
          {t('workspace.public_shares.detail.events_title')}
        </h2>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.8125rem' }}>
          {t('workspace.public_shares.detail.events_description')}
        </p>
      </div>

      {orderedEvents.length > 0 ? (
        <div
          style={{
            overflowX: 'auto',
            opacity: reordering ? 0.6 : 1,
            transition: 'opacity var(--nf-duration-fast, 120ms) ease',
          }}
          aria-busy={reordering || undefined}
        >
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragEnd={handleDragEnd}
            accessibility={{ announcements: dragAnnouncements }}
          >
            <table style={{ inlineSize: '100%', borderCollapse: 'collapse', fontSize: '0.875rem' }}>
              <thead>
                <tr style={{ textAlign: 'start', color: 'var(--nf-color-fg-muted)' }}>
                  <th
                    scope="col"
                    style={{ inlineSize: '2rem', padding: '0.5rem 0.25rem' }}
                    aria-label={dragHandleLabel}
                  />
                  <th scope="col" style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                    {t('workspace.public_shares.detail.table.event')}
                  </th>
                  <th scope="col" style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                    {t('workspace.public_shares.detail.table.when')}
                  </th>
                  <th scope="col" style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                    {t('workspace.public_shares.detail.table.calendar')}
                  </th>
                  <th scope="col" style={{ padding: '0.5rem 0.75rem', textAlign: 'end' }}>
                    {t('workspace.public_shares.detail.table.actions')}
                  </th>
                </tr>
              </thead>
              <SortableContext
                items={orderedEvents.map((e) => e.linkId)}
                strategy={verticalListSortingStrategy}
              >
                <tbody>
                  {orderedEvents.map((event) => (
                    <SortableRow
                      key={event.linkId}
                      event={event}
                      locale={locale}
                      allDayLabel={allDayLabel}
                      undatedLabel={t('workspace.public_shares.detail.event_undated')}
                      detachLabel={t('workspace.public_shares.detail.detach')}
                      handleLabel={dragHandleLabel}
                      disabled={reordering}
                      onDetach={handleDetach}
                    />
                  ))}
                </tbody>
              </SortableContext>
            </table>
          </DndContext>
        </div>
      ) : (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspace.public_shares.detail.empty_events')}
        </p>
      )}

      <Suspense
        fallback={
          <div style={{ display: 'none' }}>
            <Skeleton style={{ blockSize: '1px' }} />
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
  locale: string;
  allDayLabel: string;
  undatedLabel: string;
  detachLabel: string;
  handleLabel: string;
  disabled: boolean;
  onDetach: (event: ShareEvent) => Promise<void>;
}

/**
 * One draggable row in the attached-events table. The drag handle is a dedicated
 * button in the first cell so the rest of the row (especially the Remove action)
 * stays clickable without triggering drags. Keyboard users focus the handle and
 * use Space + arrow keys via `@dnd-kit`'s KeyboardSensor.
 */
function SortableRow({
  event,
  locale,
  allDayLabel,
  undatedLabel,
  detachLabel,
  handleLabel,
  disabled,
  onDetach,
}: SortableRowProps): ReactElement {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: event.linkId,
    disabled,
  });

  const rowStyle: CSSProperties = {
    borderBlockStart: '1px solid var(--nf-color-border)',
    transform: CSS.Transform.toString(transform),
    transition,
    backgroundColor: isDragging ? 'var(--nf-color-surface-raised, transparent)' : undefined,
    boxShadow: isDragging ? 'var(--nf-shadow-sm, 0 1px 2px rgba(0,0,0,0.08))' : undefined,
    position: isDragging ? 'relative' : undefined,
    zIndex: isDragging ? 1 : undefined,
  };

  return (
    <tr ref={setNodeRef} style={rowStyle}>
      <td style={{ padding: '0.25rem 0.25rem 0.25rem 0.5rem', verticalAlign: 'middle' }}>
        <button
          type="button"
          aria-label={handleLabel}
          disabled={disabled}
          {...attributes}
          {...listeners}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            inlineSize: '1.5rem',
            blockSize: '1.5rem',
            padding: 0,
            background: 'transparent',
            border: 'none',
            color: 'var(--nf-color-fg-muted)',
            cursor: disabled ? 'not-allowed' : 'grab',
            touchAction: 'none',
            borderRadius: 'var(--nf-radius-sm, 4px)',
          }}
        >
          <GripVertical size={14} aria-hidden />
        </button>
      </td>
      <td style={{ padding: '0.75rem' }}>
        <div style={{ fontWeight: 500 }}>{event.title}</div>
        {event.location ? (
          <div style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>
            {event.location}
          </div>
        ) : null}
      </td>
      <td style={{ padding: '0.75rem' }}>
        {event.startAt ? formatWhen(event, locale, allDayLabel) : undatedLabel}
      </td>
      <td style={{ padding: '0.75rem' }}>{event.calendarName}</td>
      <td
        style={{
          padding: '0.75rem',
          textAlign: 'end',
          whiteSpace: 'nowrap',
        }}
      >
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            void onDetach(event);
          }}
        >
          {detachLabel}
        </Button>
      </td>
    </tr>
  );
}

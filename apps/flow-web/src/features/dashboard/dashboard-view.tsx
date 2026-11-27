/**
 * DashboardView — main dashboard view that renders a grid of
 * customizable widgets. Supports an edit mode with reordering by pointer
 * drag or by move buttons, and widget removal.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Icon from '@nodate-flow/ui/icon';
import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useQueryClient } from '@tanstack/react-query';
import { LayoutDashboard, Pencil, Plus } from 'lucide-react';
import { type PointerEvent, type ReactElement, Suspense, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import AddWidgetDialog from './add-widget-dialog';
import {
  dashboardKeys,
  useDeleteWidget,
  useUpdateWidgetPosition,
  useWidgetsQuery,
  type WidgetItem,
} from './api';
import styles from './dashboard.module.css';
import WidgetCard from './widget-card';

/** Pointer travel before a press becomes a drag, so a plain click never lifts a card. */
const DRAG_THRESHOLD_PX = 4;

interface DragState {
  id: string;
  x: number;
  y: number;
  over: { id: string; side: 'before' | 'after' } | null;
}

/** The card under the pointer (other than the dragged one) and which half it is in. */
function findDropTarget(e: PointerEvent<HTMLDivElement>, draggedId: string): DragState['over'] {
  for (const el of e.currentTarget.ownerDocument.elementsFromPoint(e.clientX, e.clientY)) {
    const card = el.closest<HTMLElement>('[data-widget-id]');
    const id = card?.dataset.widgetId;
    if (!card || !id || id === draggedId) continue;
    const rect = card.getBoundingClientRect();
    const grid = card.parentElement?.getBoundingClientRect();
    // A card spanning the whole row (single-column layout) is split top/bottom instead.
    if (grid && rect.width >= grid.width * 0.9) {
      return { id, side: e.clientY < rect.top + rect.height / 2 ? 'before' : 'after' };
    }
    const rtl = getComputedStyle(card).direction === 'rtl';
    const startHalf = rtl
      ? e.clientX > rect.left + rect.width / 2
      : e.clientX < rect.left + rect.width / 2;
    return { id, side: startHalf ? 'before' : 'after' };
  }
  return null;
}

// ---------------------------------------------------------------------------
// Inner widget grid (inside Suspense boundary)
// ---------------------------------------------------------------------------

interface WidgetGridProps {
  wsId: string;
  editing: boolean;
  onAddWidget: () => void;
}

function WidgetGrid({ wsId, editing, onAddWidget }: WidgetGridProps): ReactElement {
  const { t } = useTranslation('dashboard');
  const { data: widgets } = useWidgetsQuery(wsId);
  const deleteWidget = useDeleteWidget(wsId);
  const updatePosition = useUpdateWidgetPosition(wsId);
  const stagger = useFirstLoadStagger();
  const presence = usePresenceList(widgets, (widget) => widget.id);

  const queryClient = useQueryClient();
  const [drag, setDrag] = useState<DragState | null>(null);
  const [settledId, setSettledId] = useState<string | null>(null);
  const pendingRef = useRef<{
    id: string;
    pointerId: number;
    startX: number;
    startY: number;
  } | null>(null);

  // Moves one widget to `toIndex` in display order. The list is renumbered so ties in
  // `sortWeight` (new widgets all start at 0) cannot leave the order ambiguous.
  const commitMove = (widgetId: string, toIndex: number): void => {
    const from = widgets.findIndex((w) => w.id === widgetId);
    if (from < 0 || from === toIndex) return;
    const next = widgets.slice();
    const [moved] = next.splice(from, 1);
    if (!moved) return;
    next.splice(toIndex, 0, moved);
    const renumbered = next.map((w, i) => ({ ...w, sortWeight: i }));
    queryClient.setQueryData(dashboardKeys.list(wsId), renumbered);
    setSettledId(widgetId);
    const before = new Map(widgets.map((w) => [w.id, w.sortWeight]));
    void Promise.all(
      renumbered
        .filter((w) => before.get(w.id) !== w.sortWeight)
        .map((w) =>
          updatePosition.mutateAsync({
            widgetId: w.id,
            positionX: w.positionX,
            positionY: w.positionY,
            width: w.width,
            height: w.height,
            sortWeight: w.sortWeight,
          }),
        ),
    ).catch((err: unknown) => {
      void queryClient.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'update_position_error'),
      });
    });
  };

  const handlePointerDown = (widget: WidgetItem, e: PointerEvent<HTMLDivElement>): void => {
    if (!editing || e.button !== 0) return;
    // Buttons inside the card keep their own click behaviour.
    if (e.target instanceof Element && e.target.closest('button')) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    pendingRef.current = {
      id: widget.id,
      pointerId: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
    };
  };

  const handlePointerMove = (widget: WidgetItem, e: PointerEvent<HTMLDivElement>): void => {
    const start = pendingRef.current;
    if (!start || start.id !== widget.id || start.pointerId !== e.pointerId) return;
    const x = e.clientX - start.startX;
    const y = e.clientY - start.startY;
    if (!drag && Math.hypot(x, y) < DRAG_THRESHOLD_PX) return;
    setSettledId(null);
    setDrag({ id: widget.id, x, y, over: findDropTarget(e, widget.id) });
  };

  const finishDrag = (widget: WidgetItem, e: PointerEvent<HTMLDivElement>, drop: boolean): void => {
    const start = pendingRef.current;
    if (!start || start.id !== widget.id || start.pointerId !== e.pointerId) return;
    pendingRef.current = null;
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId);
    }
    const over = drag?.over ?? null;
    setDrag(null);
    if (!drop || !over) return;
    const others = widgets.filter((w) => w.id !== widget.id);
    const targetIndex = others.findIndex((w) => w.id === over.id);
    if (targetIndex < 0) return;
    commitMove(widget.id, over.side === 'before' ? targetIndex : targetIndex + 1);
  };

  const handleRemove = async (widgetId: string): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('remove_widget_confirm'),
      confirmLabel: t('remove_widget'),
      tone: 'danger',
    });
    if (!confirmed) return;
    try {
      await deleteWidget.mutateAsync(widgetId);
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'delete_widget_error') });
    }
  };

  if (presence.entries.length === 0) {
    return (
      <div className={styles.empty}>
        <div className={styles.widgetBodyIcon}>
          <Icon icon={LayoutDashboard} decorative size={48} />
        </div>
        <p className={styles.emptyTitle}>{t('empty')}</p>
        <p className={styles.emptyDescription}>{t('empty_description')}</p>
        <Button variant="primary" onClick={onAddWidget}>
          <Icon icon={Plus} decorative size={14} />
          {t('add_widget')}
        </Button>
      </div>
    );
  }

  return (
    <div className={styles.grid}>
      {presence.entries.map((entry, index) => (
        <WidgetCard
          key={entry.key}
          widget={entry.item}
          workspaceId={wsId}
          present={entry.present}
          onExited={() => {
            presence.onExited(entry.key);
          }}
          enterStyle={stagger(index)}
          editing={editing}
          dragging={drag?.id === entry.key}
          dragOffset={drag?.id === entry.key ? { x: drag.x, y: drag.y } : undefined}
          dropSide={drag?.over?.id === entry.key ? drag.over.side : undefined}
          settling={settledId === entry.key}
          onSettled={() => {
            setSettledId(null);
          }}
          canMoveEarlier={widgets.findIndex((w) => w.id === entry.key) > 0}
          canMoveLater={
            widgets.findIndex((w) => w.id === entry.key) >= 0 &&
            widgets.findIndex((w) => w.id === entry.key) < widgets.length - 1
          }
          onMoveEarlier={() => {
            commitMove(entry.key, widgets.findIndex((w) => w.id === entry.key) - 1);
          }}
          onMoveLater={() => {
            commitMove(entry.key, widgets.findIndex((w) => w.id === entry.key) + 1);
          }}
          removing={deleteWidget.isPending && deleteWidget.variables === entry.key}
          onRemove={() => {
            void handleRemove(entry.key);
          }}
          onPointerDown={(e) => {
            handlePointerDown(entry.item, e);
          }}
          onPointerMove={(e) => {
            handlePointerMove(entry.item, e);
          }}
          onPointerUp={(e) => {
            finishDrag(entry.item, e, true);
          }}
          onPointerCancel={(e) => {
            finishDrag(entry.item, e, false);
          }}
        />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface DashboardViewProps {
  workspaceId: string;
  /** Shown under the heading so the reader knows whose widgets these are. */
  workspaceName: string;
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function DashboardView({
  workspaceId,
  workspaceName,
}: DashboardViewProps): ReactElement {
  const { t } = useTranslation('dashboard');
  const [editing, setEditing] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <div className={styles.heading}>
          <h2 className={styles.title}>{t('title')}</h2>
          <p className={styles.scope}>{t('scope', { name: workspaceName })}</p>
        </div>
        {/* Adding a widget is a layout edit, so it only appears in edit mode. */}
        <div className={styles.actions}>
          {editing ? (
            <Button
              onClick={() => {
                setDialogOpen(true);
              }}
            >
              <Icon icon={Plus} decorative size={14} />
              {t('add_widget')}
            </Button>
          ) : null}
          <Button
            variant={editing ? 'primary' : 'ghost'}
            onClick={() => {
              setEditing((prev) => !prev);
            }}
          >
            <Icon icon={Pencil} decorative size={14} />
            {editing ? t('done_editing') : t('edit_layout')}
          </Button>
        </div>
      </div>

      <Suspense
        fallback={
          <div className={styles.grid} aria-busy="true">
            <Skeleton className={styles.widgetSkeleton} />
            <Skeleton className={styles.widgetSkeleton} />
            <Skeleton className={styles.widgetSkeleton} />
          </div>
        }
      >
        <WidgetGrid
          wsId={workspaceId}
          editing={editing}
          onAddWidget={() => {
            setDialogOpen(true);
          }}
        />
      </Suspense>

      <AddWidgetDialog
        workspaceId={workspaceId}
        open={dialogOpen}
        onClose={() => {
          setDialogOpen(false);
        }}
      />
    </div>
  );
}

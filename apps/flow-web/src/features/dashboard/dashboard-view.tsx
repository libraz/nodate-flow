/**
 * DashboardView — main dashboard view that renders a grid of
 * customizable widgets. Supports an edit mode with drag-to-reorder
 * (mouse events, no external library) and widget removal.
 */

import Icon from '@nodate-flow/ui/icon';
import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { LayoutDashboard, Pencil, Plus } from 'lucide-react';
import { type ReactElement, Suspense, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AddWidgetDialog from './add-widget-dialog';
import { useDeleteWidget, useUpdateWidgetPosition, useWidgetsQuery, type WidgetItem } from './api';
import styles from './dashboard.module.css';
import WidgetCard from './widget-card';

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

  const [draggingId, setDraggingId] = useState<string | null>(null);
  const dragStartRef = useRef<{ id: string; startX: number; startY: number } | null>(null);
  const listenersRef = useRef<{
    move: (e: MouseEvent) => void;
    up: (e: MouseEvent) => void;
  } | null>(null);

  // Clean up drag listeners on unmount to prevent memory leaks
  useEffect(() => {
    return () => {
      if (listenersRef.current) {
        document.removeEventListener('mousemove', listenersRef.current.move);
        document.removeEventListener('mouseup', listenersRef.current.up);
        listenersRef.current = null;
      }
    };
  }, []);

  const handleMouseDown = (widget: WidgetItem, e: React.MouseEvent<HTMLDivElement>): void => {
    if (!editing) return;
    dragStartRef.current = {
      id: widget.id,
      startX: e.clientX,
      startY: e.clientY,
    };
    setDraggingId(widget.id);

    const handleMouseMove = (_moveEvent: MouseEvent): void => {
      // Visual feedback is handled via the dragging CSS class.
      // Full grid-snap repositioning would require more complex logic
      // and is deferred to a future iteration.
    };

    const handleMouseUp = (upEvent: MouseEvent): void => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      listenersRef.current = null;
      setDraggingId(null);

      const start = dragStartRef.current;
      if (!start) return;

      const dx = upEvent.clientX - start.startX;
      const dy = upEvent.clientY - start.startY;

      // Only commit position change if the drag was significant
      if (Math.abs(dx) > 40 || Math.abs(dy) > 40) {
        const colDelta = Math.round(dx / 80);
        const rowDelta = Math.round(dy / 80);
        const newX = Math.max(0, widget.positionX + colDelta);
        const newY = Math.max(0, widget.positionY + rowDelta);
        void updatePosition
          .mutateAsync({
            widgetId: widget.id,
            positionX: newX,
            positionY: newY,
            width: widget.width,
            height: widget.height,
            sortWeight: widget.sortWeight,
          })
          .catch(() => {
            toaster.show({ tone: 'danger', message: t('update_position_error') });
          });
      }

      dragStartRef.current = null;
    };

    listenersRef.current = { move: handleMouseMove, up: handleMouseUp };
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
  };

  const handleRemove = (widgetId: string): void => {
    void deleteWidget.mutateAsync(widgetId).catch(() => {
      toaster.show({ tone: 'danger', message: t('delete_widget_error') });
    });
  };

  if (widgets.length === 0) {
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
      {widgets.map((widget) => (
        <WidgetCard
          key={widget.id}
          widget={widget}
          editing={editing}
          dragging={draggingId === widget.id}
          onRemove={() => {
            handleRemove(widget.id);
          }}
          onMouseDown={(e) => {
            handleMouseDown(widget, e);
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
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function DashboardView({ workspaceId }: DashboardViewProps): ReactElement {
  const { t } = useTranslation('dashboard');
  const [editing, setEditing] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <h2 className={styles.title}>{t('title')}</h2>
        <div className={styles.actions}>
          <Button
            variant={editing ? 'primary' : 'ghost'}
            onClick={() => {
              setEditing((prev) => !prev);
            }}
          >
            <Icon icon={Pencil} decorative size={14} />
            {editing ? t('done_editing') : t('edit_layout')}
          </Button>
          <Button
            variant="primary"
            onClick={() => {
              setDialogOpen(true);
            }}
          >
            <Icon icon={Plus} decorative size={14} />
            {t('add_widget')}
          </Button>
        </div>
      </div>

      <Suspense
        fallback={
          <div
            style={{
              padding: '2rem',
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('title')}
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

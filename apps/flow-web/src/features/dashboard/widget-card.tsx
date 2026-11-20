/**
 * WidgetCard — renders a single dashboard widget with header and
 * body content based on the widget type.
 *
 * In edit mode, shows a remove button and visual resize indicator.
 */

import { usePresence } from '@nodate-flow/ui/hooks/use-presence';
import Icon from '@nodate-flow/ui/icon';
import {
  AlertTriangle,
  BarChart3,
  Bell,
  type LucideIcon,
  Radio,
  Sparkles,
  SquareKanban,
  X,
} from 'lucide-react';
import { type CSSProperties, type ReactElement, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import type { WidgetItem, WidgetType } from './api';
import styles from './dashboard.module.css';
import WidgetBody from './widgets/widget-body';

// ---------------------------------------------------------------------------
// Icon mapping
// ---------------------------------------------------------------------------

const widgetIconMap = new Map<WidgetType, LucideIcon>([
  ['task_summary', SquareKanban],
  ['burndown', BarChart3],
  ['signals_feed', Radio],
  ['ai_suggestions', Sparkles],
  ['overdue_tasks', AlertTriangle],
  ['notification_feed', Bell],
]);

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface WidgetCardProps {
  widget: WidgetItem;
  workspaceId: string;
  editing: boolean;
  dragging?: boolean | undefined;
  /** First-load stagger style from the grid (`--stagger-index`). */
  enterStyle?: CSSProperties | undefined;
  /** False once the widget is removed; the card plays its exit, then calls `onExited`. */
  present?: boolean | undefined;
  onExited?: (() => void) | undefined;
  /** True while the delete request for this widget is in flight. */
  removing?: boolean | undefined;
  onRemove?: (() => void) | undefined;
  onMouseDown?: ((e: React.MouseEvent<HTMLDivElement>) => void) | undefined;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function WidgetCard({
  widget,
  workspaceId,
  editing,
  dragging,
  enterStyle,
  present = true,
  onExited,
  removing,
  onRemove,
  onMouseDown,
}: WidgetCardProps): ReactElement | null {
  const { t } = useTranslation('dashboard');
  const presence = usePresence<HTMLDivElement>(present);

  // biome-ignore lint/correctness/useExhaustiveDependencies: fires once per completed exit; onExited is recreated every render
  useEffect(() => {
    if (!presence.mounted) onExited?.();
  }, [presence.mounted]);

  const TypeIcon = widgetIconMap.get(widget.widgetType) ?? SquareKanban;

  const classNames = [
    styles.widget,
    editing ? styles.widgetEditing : '',
    dragging ? styles.widgetDragging : '',
    'nf-focus-ring',
  ]
    .filter(Boolean)
    .join(' ');

  if (!presence.mounted) return null;

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: container only takes a pointer-drag affordance while editing; all real actions live on focusable child buttons.
    <div
      ref={presence.ref}
      data-state={presence.state}
      inert={!present}
      className={classNames}
      style={
        {
          '--widget-cols': widget.width,
          '--widget-rows': widget.height,
          ...enterStyle,
        } as CSSProperties
      }
      onMouseDown={editing ? onMouseDown : undefined}
    >
      <div className={styles.widgetHeader}>
        <div className={styles.widgetIcon}>
          <Icon icon={TypeIcon} decorative size={16} />
        </div>
        <h3 className={styles.widgetTitle}>{widget.title}</h3>
        {editing && onRemove && (
          <button
            type="button"
            className={`${styles.removeButton} nf-focus-ring`}
            onClick={(e) => {
              e.stopPropagation();
              onRemove();
            }}
            aria-label={t('remove_widget')}
            disabled={removing}
            aria-busy={removing || undefined}
          >
            <Icon icon={X} decorative size={14} />
          </button>
        )}
      </div>

      <WidgetBody widgetType={widget.widgetType} workspaceId={workspaceId} />

      {editing && (
        <div className={styles.resizeHandle} aria-hidden="true">
          <svg
            viewBox="0 0 16 16"
            fill="currentColor"
            width="16"
            height="16"
            role="img"
            aria-label={t('resize_label')}
          >
            <path d="M14 14H10V12H12V10H14V14ZM14 8H12V6H14V8ZM8 14H6V12H8V14Z" />
          </svg>
        </div>
      )}
    </div>
  );
}

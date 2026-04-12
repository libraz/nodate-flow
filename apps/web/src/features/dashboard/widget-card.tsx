/**
 * WidgetCard — renders a single dashboard widget with header and
 * placeholder body content based on the widget type.
 *
 * In edit mode, shows a remove button and visual resize indicator.
 */

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
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { WidgetItem, WidgetType } from './api';
import styles from './dashboard.module.css';

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
// i18n key mapping (static — no dynamic key construction)
// ---------------------------------------------------------------------------

const widgetTypeI18nKey = new Map<WidgetType, string>([
  ['task_summary', 'widget_type.task_summary'],
  ['burndown', 'widget_type.burndown'],
  ['signals_feed', 'widget_type.signals_feed'],
  ['ai_suggestions', 'widget_type.ai_suggestions'],
  ['overdue_tasks', 'widget_type.overdue_tasks'],
  ['notification_feed', 'widget_type.notification_feed'],
]);

// ---------------------------------------------------------------------------
// Widget body placeholder
// ---------------------------------------------------------------------------

function WidgetBodyPlaceholder({ widgetType }: { widgetType: WidgetType }): ReactElement {
  const { t } = useTranslation('dashboard');
  const TypeIcon = widgetIconMap.get(widgetType) ?? SquareKanban;

  return (
    <div className={styles.widgetBody}>
      <div className={styles.widgetBodyIcon}>
        <Icon icon={TypeIcon} decorative size={32} />
      </div>
      <span>{t(widgetTypeI18nKey.get(widgetType) ?? 'widget_type.task_summary')}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface WidgetCardProps {
  widget: WidgetItem;
  editing: boolean;
  dragging?: boolean | undefined;
  onRemove?: (() => void) | undefined;
  onMouseDown?: ((e: React.MouseEvent<HTMLDivElement>) => void) | undefined;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function WidgetCard({
  widget,
  editing,
  dragging,
  onRemove,
  onMouseDown,
}: WidgetCardProps): ReactElement {
  const { t } = useTranslation('dashboard');
  const TypeIcon = widgetIconMap.get(widget.widgetType) ?? SquareKanban;

  const classNames = [
    styles.widget,
    editing ? styles.widgetEditing : '',
    dragging ? styles.widgetDragging : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div
      className={classNames}
      style={{
        gridColumn: `span ${String(widget.width)}`,
        gridRow: `span ${String(widget.height)}`,
        position: 'relative',
      }}
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
            className={styles.removeButton}
            onClick={(e) => {
              e.stopPropagation();
              onRemove();
            }}
            aria-label={t('remove_widget')}
          >
            <Icon icon={X} decorative size={14} />
          </button>
        )}
      </div>

      <WidgetBodyPlaceholder widgetType={widget.widgetType} />

      {editing && (
        <div className={styles.resizeHandle} aria-hidden="true">
          <svg
            viewBox="0 0 16 16"
            fill="currentColor"
            width="16"
            height="16"
            role="img"
            aria-label="Resize"
          >
            <path d="M14 14H10V12H12V10H14V14ZM14 8H12V6H14V8ZM8 14H6V12H8V14Z" />
          </svg>
        </div>
      )}
    </div>
  );
}

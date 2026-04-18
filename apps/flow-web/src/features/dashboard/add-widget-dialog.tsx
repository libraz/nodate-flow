/**
 * AddWidgetDialog — modal for selecting a widget type and title,
 * then creating a new dashboard widget.
 */

import Icon from '@nodate-flow/ui/icon';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import {
  AlertTriangle,
  BarChart3,
  Bell,
  type LucideIcon,
  Radio,
  Sparkles,
  SquareKanban,
} from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type WidgetType, useCreateWidget } from './api';
import styles from './dashboard.module.css';

// ---------------------------------------------------------------------------
// Widget type metadata
// ---------------------------------------------------------------------------

interface WidgetTypeMeta {
  type: WidgetType;
  icon: LucideIcon;
  defaultWidth: number;
  defaultHeight: number;
}

const widgetTypes: WidgetTypeMeta[] = [
  { type: 'task_summary', icon: SquareKanban, defaultWidth: 4, defaultHeight: 2 },
  { type: 'burndown', icon: BarChart3, defaultWidth: 6, defaultHeight: 3 },
  { type: 'signals_feed', icon: Radio, defaultWidth: 4, defaultHeight: 3 },
  { type: 'ai_suggestions', icon: Sparkles, defaultWidth: 4, defaultHeight: 2 },
  { type: 'overdue_tasks', icon: AlertTriangle, defaultWidth: 4, defaultHeight: 2 },
  { type: 'notification_feed', icon: Bell, defaultWidth: 4, defaultHeight: 3 },
];

// ---------------------------------------------------------------------------
// i18n key map (static keys only — no dynamic construction)
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
// Props
// ---------------------------------------------------------------------------

export interface AddWidgetDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function AddWidgetDialog({
  workspaceId,
  open,
  onClose,
}: AddWidgetDialogProps): ReactElement {
  const { t } = useTranslation('dashboard');
  const create = useCreateWidget(workspaceId);

  const [selectedType, setSelectedType] = useState<WidgetType | null>(null);
  const [title, setTitle] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setSelectedType(null);
    setTitle('');
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!selectedType) return;

    const meta = widgetTypes.find((wt) => wt.type === selectedType);
    if (!meta) return;

    const finalTitle =
      title.trim() || t(widgetTypeI18nKey.get(selectedType) ?? 'widget_type.task_summary');

    setSubmitting(true);
    try {
      await create.mutateAsync({
        input: {
          widgetType: selectedType,
          title: finalTitle,
          width: meta.defaultWidth,
          height: meta.defaultHeight,
        },
      });
      reset();
      onClose();
      toaster.show({ tone: 'success', message: t('add_widget') });
    } catch {
      toaster.show({ tone: 'danger', message: t('add_widget') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('add_widget')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        className={styles.dialogForm}
      >
        {/* Widget type selector */}
        <FormField label={t('select_type')}>
          {() => (
            <div className={styles.typeGrid} role="radiogroup" aria-label={t('select_type')}>
              {widgetTypes.map((wt) => {
                const selected = selectedType === wt.type;
                const cardClass = [styles.typeCard, selected ? styles.typeCardSelected : '']
                  .filter(Boolean)
                  .join(' ');
                return (
                  <button
                    key={wt.type}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    className={cardClass}
                    onClick={() => {
                      setSelectedType(wt.type);
                    }}
                  >
                    <div className={styles.typeCardIcon}>
                      <Icon icon={wt.icon} decorative size={24} />
                    </div>
                    <span className={styles.typeCardLabel}>
                      {t(widgetTypeI18nKey.get(wt.type) ?? 'widget_type.task_summary')}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </FormField>

        {/* Title input */}
        <FormField label={t('widget_title_label')}>
          {(control) => (
            <Input
              {...control}
              value={title}
              placeholder={
                selectedType
                  ? t(widgetTypeI18nKey.get(selectedType) ?? 'widget_type.task_summary')
                  : t('widget_title_label')
              }
              onChange={(e) => {
                setTitle(e.target.value);
              }}
            />
          )}
        </FormField>

        {/* Actions */}
        <div className={styles.dialogActions}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('done_editing')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting || !selectedType}>
            {t('add_widget')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

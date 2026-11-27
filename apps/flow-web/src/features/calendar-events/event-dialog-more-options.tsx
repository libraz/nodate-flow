import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './event-dialog.module.css';
import type { NotificationPreset } from './lib/notification-preset';
import { type RecurrencePreset, weekdayName } from './lib/recurrence-preset';

const RECURRENCE_PRESET_KEYS = {
  none: 'recurrence.preset.none',
  daily: 'recurrence.preset.daily',
  weekdays: 'recurrence.preset.weekdays',
  weekly: 'recurrence.preset.weekly',
  monthly: 'recurrence.preset.monthly',
  yearly: 'recurrence.preset.yearly',
  custom: 'recurrence.preset.custom',
} as const satisfies Record<RecurrencePreset, string>;

const NOTIFICATION_PRESET_KEYS = {
  none: 'notification.preset.none',
  // biome-ignore lint/style/useNamingConvention: matches NotificationPreset literal union
  at_time: 'notification.preset.at_time',
  '5min': 'notification.preset.5min',
  '10min': 'notification.preset.10min',
  '15min': 'notification.preset.15min',
  '30min': 'notification.preset.30min',
  '1hour': 'notification.preset.1hour',
  '1day': 'notification.preset.1day',
} as const satisfies Record<NotificationPreset, string>;

interface EventDialogMoreOptionsProps {
  expanded: boolean;
  onToggle: () => void;
  recurrence: RecurrencePreset;
  onRecurrenceChange: (value: RecurrencePreset) => void;
  notification: NotificationPreset;
  onNotificationChange: (value: NotificationPreset) => void;
  memo: string;
  onMemoChange: (value: string) => void;
  startDate: string;
  isTask: boolean;
  detailLoading: boolean;
  detailsHidden: boolean;
}

/** The "more options" disclosure: recurrence, notification and memo / description. */
export default function EventDialogMoreOptions({
  expanded,
  onToggle,
  recurrence,
  onRecurrenceChange,
  notification,
  onNotificationChange,
  memo,
  onMemoChange,
  startDate,
  isTask,
  detailLoading,
  detailsHidden,
}: EventDialogMoreOptionsProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';
  const disclosureId = useId();

  return (
    <>
      {/* More options disclosure */}
      <Tooltip content={t('action.moreOptionsHint')} placement="right">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onToggle}
          className={styles.disclosureToggle}
          aria-expanded={expanded}
          aria-controls={disclosureId}
        >
          {t('action.moreOptions')}
        </Button>
      </Tooltip>
      <Collapse open={expanded} id={disclosureId}>
        <div className={styles.disclosurePanel}>
          <FormField label={t('field.recurrence')} description={t('field.recurrenceHint')}>
            {(control) => (
              <Select
                {...control}
                value={recurrence}
                onChange={(e) => onRecurrenceChange(e.currentTarget.value as RecurrencePreset)}
              >
                {/*
                `custom` is listed only while it is the current
                value: it describes a stored rule rather than a
                choice, so offering it on an event that has no
                such rule would be an option that does nothing.
              */}
                {(recurrence === 'custom'
                  ? ([
                      'custom',
                      'none',
                      'daily',
                      'weekdays',
                      'weekly',
                      'monthly',
                      'yearly',
                    ] as const)
                  : (['none', 'daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const)
                ).map((v) => (
                  <option key={v} value={v}>
                    {/*
                      The weekly option names the day it repeats
                      on, which is the start date's weekday — the
                      other presets carry no placeholder and
                      ignore the value.
                    */}
                    {t(RECURRENCE_PRESET_KEYS[v], { day: weekdayName(startDate, locale) })}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
          <FormField label={t('field.notification')} description={t('field.notificationHint')}>
            {(control) => (
              <Select
                {...control}
                value={notification}
                onChange={(e) => onNotificationChange(e.currentTarget.value as NotificationPreset)}
              >
                {(
                  ['none', 'at_time', '5min', '10min', '15min', '30min', '1hour', '1day'] as const
                ).map((v) => (
                  <option key={v} value={v}>
                    {t(NOTIFICATION_PRESET_KEYS[v])}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
          <FormField label={isTask ? t('field.description') : t('field.memo')}>
            {(control) => (
              <Textarea
                {...control}
                value={memo}
                onChange={(e) => onMemoChange(e.currentTarget.value)}
                rows={3}
                // Typing into an empty box before the stored memo
                // lands would mark the field edited and send that
                // fragment over the real one, so the control stays
                // inert until it holds the actual value.
                disabled={detailLoading || detailsHidden}
                aria-busy={detailLoading || undefined}
              />
            )}
          </FormField>
        </div>
      </Collapse>
    </>
  );
}

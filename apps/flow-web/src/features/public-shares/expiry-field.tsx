/**
 * ExpiryField — choose between "never expires" and a date + time, shared
 * by the create dialog and the share detail editor. Times are read in the
 * browser's local zone, matching how expiries are displayed.
 */

import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import FormField from '@nodate-flow/ui/primitives/form-field';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import TimePicker from '@nodate-flow/ui/primitives/time-picker';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDateOnly } from '../../lib/format';
import { useWeekStart } from '../../lib/use-week-start';
import styles from './expiry-field.module.css';

export type ExpiryMode = 'never' | 'at';

export interface ExpiryDraft {
  mode: ExpiryMode;
  /** Local date, YYYY-MM-DD. */
  date: string;
  /** Local time, HH:MM. */
  time: string;
}

const DEFAULT_TIME = '09:00';
const DEFAULT_LEAD_DAYS = 7;

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

/** Local calendar date of `d` as YYYY-MM-DD. */
export function toLocalDateString(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Draft for a share with no expiry; the date/time are the pre-filled suggestion. */
export function emptyExpiryDraft(now: Date = new Date()): ExpiryDraft {
  const suggested = new Date(now);
  suggested.setDate(suggested.getDate() + DEFAULT_LEAD_DAYS);
  return { mode: 'never', date: toLocalDateString(suggested), time: DEFAULT_TIME };
}

/** Draft seeded from a stored expiry (unix seconds), or empty when none. */
export function draftFromEpoch(epochSec: number | undefined): ExpiryDraft {
  if (!epochSec) return emptyExpiryDraft();
  const d = new Date(epochSec * 1000);
  return {
    mode: 'at',
    date: toLocalDateString(d),
    time: `${pad(d.getHours())}:${pad(d.getMinutes())}`,
  };
}

/** Unix seconds for a draft, `undefined` for "never", `NaN` when the pair is invalid. */
export function draftToEpoch(draft: ExpiryDraft): number | undefined {
  if (draft.mode === 'never') return undefined;
  const ms = new Date(`${draft.date}T${draft.time}:00`).getTime();
  return Math.floor(ms / 1000);
}

export interface ExpiryFieldProps {
  draft: ExpiryDraft;
  onChange: (next: ExpiryDraft) => void;
  /** Already-translated error shown under the control. */
  error?: string | undefined;
}

export default function ExpiryField({ draft, onChange, error }: ExpiryFieldProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekStart = useWeekStart();
  const weekdayLabelsRaw = tCommon('common.date.weekdays', { returnObjects: true });
  const weekdayLabels: string[] = Array.isArray(weekdayLabelsRaw) ? weekdayLabelsRaw : [];

  return (
    <FormField
      label={t('workspace.public_shares.expiry.label')}
      description={t('workspace.public_shares.expiry.help')}
      error={error}
    >
      {() => (
        <div className={styles.field}>
          <SegmentedControl<ExpiryMode>
            ariaLabel={t('workspace.public_shares.expiry.label')}
            value={draft.mode}
            onChange={(mode) => {
              onChange({ ...draft, mode });
            }}
            options={[
              { value: 'never', label: t('workspace.public_shares.expiry.never') },
              { value: 'at', label: t('workspace.public_shares.expiry.at') },
            ]}
          />
          {draft.mode === 'at' ? (
            <div className={styles.pickers}>
              <DatePicker
                variant="field"
                value={draft.date}
                minDate={toLocalDateString(new Date())}
                onChange={(date) => {
                  onChange({ ...draft, date });
                }}
                weekdayLabels={weekdayLabels}
                weekStart={weekStart}
                formatMonthYear={(year, month) => tCommon('common.date.monthYear', { year, month })}
                prevLabel={tCommon('calendar.prev')}
                nextLabel={tCommon('calendar.next')}
                triggerLabel={
                  draft.date
                    ? formatDateOnly(draft.date, locale)
                    : tCommon('common.date.placeholder')
                }
              />
              <TimePicker
                variant="field"
                value={draft.time}
                onChange={(time) => {
                  onChange({ ...draft, time });
                }}
                listLabel={t('workspace.public_shares.expiry.time_label')}
              />
            </div>
          ) : null}
        </div>
      )}
    </FormField>
  );
}

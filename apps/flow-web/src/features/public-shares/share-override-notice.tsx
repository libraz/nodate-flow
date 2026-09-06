/**
 * ShareOverrideNotice — the row that follows a published series whose share
 * still advertises an occurrence at a start it has moved away from.
 *
 * Suppressing a moved occurrence on the public page is scoped to what the
 * share publishes: an occurrence whose replacement is not attached keeps
 * rendering at its original time, which is the time an outside visitor acts
 * on. The publisher is the only person who can still fix that, and the
 * editor is where they are, so the warning belongs beside the series rather
 * than on the page that is already wrong.
 *
 * A private replacement is warned about but not offered as a one-click fix:
 * attaching it is refused, and a button that only ever errors reads as a
 * broken page rather than as a rule.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { AlertTriangle } from 'lucide-react';
import type { CSSProperties, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { ShareOverrideWarning } from './api';
import styles from './share-override-notice.module.css';

export interface ShareOverrideNoticeProps {
  /** Occurrences of one series the share still shows at their old start. */
  warnings: ShareOverrideWarning[];
  /** Whether the series is all-day, so a clock time is noise rather than help. */
  allDay: boolean;
  locale: string;
  /** Table width, so the notice spans the row it belongs to. */
  columnCount: number;
  /** True while an attach is in flight, so the fix is not offered twice. */
  busy: boolean;
  /** Drag transform of the series row, so the two move together. */
  rowStyle: CSSProperties;
  onAttach: (warning: ShareOverrideWarning) => void;
}

/**
 * A moment on the same clock the rest of the editor uses: medium date, short
 * time, dropped entirely for an all-day series where only the day moved.
 */
function formatMoment(seconds: number, locale: string, allDay: boolean): string {
  const at = new Date(seconds * 1000);
  const date = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(at);
  if (allDay) return date;
  return `${date} ${new Intl.DateTimeFormat(locale, { timeStyle: 'short' }).format(at)}`;
}

export default function ShareOverrideNotice({
  warnings,
  allDay,
  locale,
  columnCount,
  busy,
  rowStyle,
  onAttach,
}: ShareOverrideNoticeProps): ReactElement {
  const { t } = useTranslation('settings');

  return (
    <tr style={rowStyle}>
      <td className={styles.cell} colSpan={columnCount}>
        <div className={styles.notice} role="status">
          <AlertTriangle size={16} aria-hidden className={styles.icon} />
          <div className={styles.body}>
            <p className={styles.label}>
              {t('workspace.public_shares.detail.override_notice.label')}
            </p>
            <ul className={styles.list}>
              {warnings.map((warning) => {
                const from = formatMoment(warning.originalStart, locale, allDay);
                const to = formatMoment(warning.startAt, locale, allDay);
                if (warning.visibility === 'confidential') {
                  return (
                    <li key={warning.eventId} className={styles.item}>
                      <span>
                        {t('workspace.public_shares.detail.override_notice.confidential', {
                          from,
                          to,
                        })}
                      </span>
                    </li>
                  );
                }
                return (
                  <li key={warning.eventId} className={styles.item}>
                    <span>
                      {t('workspace.public_shares.detail.override_notice.moved', { from, to })}
                    </span>
                    <Button
                      type="button"
                      variant="default"
                      size="sm"
                      className={styles.action}
                      disabled={busy}
                      onClick={() => {
                        onAttach(warning);
                      }}
                    >
                      {t('workspace.public_shares.detail.override_notice.attach')}
                    </Button>
                  </li>
                );
              })}
            </ul>
          </div>
        </div>
      </td>
    </tr>
  );
}

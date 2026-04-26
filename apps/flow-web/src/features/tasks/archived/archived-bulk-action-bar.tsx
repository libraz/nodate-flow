/**
 * ArchivedBulkActionBar — slide-up bottom bar shown when n > 0 rows
 * are selected. The slide-up animation is CSS-driven and respects
 * `prefers-reduced-motion` (see archived.module.css).
 *
 * The bar is keyboard-reachable (Tab order through cancel + primary
 * action), labelled as a `<region>` so AT users can locate it through
 * landmark navigation, and the count text is wrapped in `aria-live`
 * so screen readers announce selection deltas without re-narrating
 * the whole region.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';

export interface ArchivedBulkActionBarProps {
  count: number;
  busy: boolean;
  onUnarchive: () => void;
  onCancel: () => void;
}

export default function ArchivedBulkActionBar({
  count,
  busy,
  onUnarchive,
  onCancel,
}: ArchivedBulkActionBarProps): ReactElement | null {
  const { t } = useTranslation('archive');
  if (count === 0) return null;
  return (
    <div className={styles.bulkBarWrap}>
      <div role="region" aria-label={t('bulk.selectedCount', { count })} className={styles.bulkBar}>
        <span className={styles.bulkBarCount} aria-live="polite">
          {t('bulk.selectedCount', { count })}
        </span>
        <span aria-hidden className={styles.bulkBarSep} />
        <button type="button" className={styles.bulkBarBtn} onClick={onCancel} disabled={busy}>
          {t('bulk.cancel')}
        </button>
        <button
          type="button"
          className={`${styles.bulkBarBtn} ${styles.bulkBarBtnPrimary}`}
          onClick={onUnarchive}
          disabled={busy}
          aria-busy={busy}
        >
          {t('bulk.unarchiveCount', { count })}
        </button>
      </div>
    </div>
  );
}

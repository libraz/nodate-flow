/**
 * ReplayPanel — 3.WEB-3 timeline replay UI.
 *
 * Shows the recomputed derived_state next to the stored value and
 * flags drift. Uses {@link useTaskReplayQuery} under the hood.
 *
 * Visual layout:
 * - Two label-value rows (Replayed / Stored) rendered as a 2-column grid
 *   with hairline separators. Each value is a {@link Chip} so the two
 *   states can be compared at a glance even when their labels match.
 * - When drift is detected, the stored chip switches to a warning tone
 *   so the row signals disagreement without relying on the label alone.
 * - A third row surfaces the match / drift summary as a chip with an
 *   inline glyph ("✓" for match, "⚠" for drift) and stays
 *   announced via `aria-live="polite"` when it updates.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Chip from '@nodate-flow/ui/primitives/chip';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskDerivedState } from '../tasks/api';
import { STATE_KEY } from '../tasks/constants';
import { useTaskReplayQuery } from './replay-api';
import styles from './replay-panel.module.css';

export interface ReplayPanelProps {
  taskId: string;
}

/**
 * Resolve the raw replay state id (e.g. "waiting") to the same localized
 * label the main task State field uses (e.g. "In progress"). Falls back
 * to the raw id if it is not a known derived state.
 */
function useStateLabel(): (raw: string) => string {
  const { t } = useTranslation('common');
  return (raw: string): string => {
    const key = STATE_KEY[raw as TaskDerivedState];
    if (!key) return raw;
    return t(key);
  };
}

export default function ReplayPanel({ taskId }: ReplayPanelProps): ReactElement {
  const { t } = useTranslation('constraints');
  const labelFor = useStateLabel();
  const q = useTaskReplayQuery(taskId);
  const drift = q.data != null && !q.data.equivalent;

  const bodyState = q.data ? 'data' : q.error ? 'error' : 'loading';

  return (
    <section aria-label={t('replay.title')} className={styles.panel}>
      <header className={styles.header}>
        <h3 className={styles.title}>{t('replay.title')}</h3>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => void q.refetch()}
          disabled={q.isFetching}
          aria-busy={q.isFetching || undefined}
        >
          {t('replay.refresh')}
        </Button>
      </header>
      <div key={bodyState} className={styles.body}>
        {q.isLoading ? (
          <div aria-busy="true">
            <span className="sr-only">{t('replay.loading')}</span>
            <Skeleton className={styles.skeletonRow} />
            <Skeleton className={styles.skeletonRow} />
            <Skeleton className={styles.skeletonRow} />
          </div>
        ) : null}
        {q.error ? (
          <p role="alert" className={styles.error}>
            {t('replay.error')}
          </p>
        ) : null}
        {q.data ? (
          <dl className={styles.list}>
            <div className={styles.row}>
              <dt className={styles.label}>{t('replay.derived')}</dt>
              <dd className={styles.value}>
                <Chip tone="neutral">{labelFor(q.data.derivedState)}</Chip>
              </dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>{t('replay.stored')}</dt>
              <dd className={styles.value}>
                <Chip tone={drift ? 'warning' : 'neutral'}>{labelFor(q.data.stored)}</Chip>
              </dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>
                {q.data.equivalent ? t('replay.equivalent') : t('replay.drift')}
              </dt>
              <dd className={styles.value} aria-live="polite">
                <Chip tone={q.data.equivalent ? 'success' : 'danger'}>
                  <span aria-hidden="true">{q.data.equivalent ? '✓' : '⚠'}</span>
                  <span>{q.data.equivalent ? t('replay.equivalent') : t('replay.drift')}</span>
                </Chip>
              </dd>
            </div>
          </dl>
        ) : null}
      </div>
    </section>
  );
}

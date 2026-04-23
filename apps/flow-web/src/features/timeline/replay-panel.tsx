/**
 * ReplayPanel — 3.WEB-3 timeline replay UI.
 *
 * Shows the recomputed derived_state next to the stored value and
 * flags drift. Uses {@link useTaskReplayQuery} under the hood.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskDerivedState } from '../tasks/api';
import { STATE_KEY } from '../tasks/constants';
import { useTaskReplayQuery } from './replay-api';

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

  return (
    <section aria-label={t('replay.title')}>
      <h3>{t('replay.title')}</h3>
      {q.isLoading ? <p>{t('replay.loading')}</p> : null}
      {q.error ? <p role="alert">{t('replay.error')}</p> : null}
      {q.data ? (
        <dl>
          <dt>{t('replay.derived')}</dt>
          <dd>
            <code>{labelFor(q.data.derivedState)}</code>
          </dd>
          <dt>{t('replay.stored')}</dt>
          <dd>
            <code>{labelFor(q.data.stored)}</code>
          </dd>
          <dt>{q.data.equivalent ? t('replay.equivalent') : t('replay.drift')}</dt>
          <dd aria-live="polite">{q.data.equivalent ? '✓' : '⚠'}</dd>
        </dl>
      ) : null}
      <Button type="button" onClick={() => void q.refetch()} disabled={q.isFetching}>
        {t('replay.refresh')}
      </Button>
    </section>
  );
}

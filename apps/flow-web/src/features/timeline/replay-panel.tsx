/**
 * ReplayPanel — 3.WEB-3 timeline replay UI.
 *
 * Shows the recomputed derived_state next to the stored value and
 * flags drift. Uses {@link useTaskReplayQuery} under the hood.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useTaskReplayQuery } from './replay-api';

export interface ReplayPanelProps {
  taskId: string;
}

export default function ReplayPanel({ taskId }: ReplayPanelProps): ReactElement {
  const { t } = useTranslation('constraints');
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
            <code>{q.data.derivedState}</code>
          </dd>
          <dt>{t('replay.stored')}</dt>
          <dd>
            <code>{q.data.stored}</code>
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

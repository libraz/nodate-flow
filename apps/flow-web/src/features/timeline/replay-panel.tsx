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
import type { CSSProperties, ReactElement } from 'react';
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

/**
 * Style for a single label/value row. Uses a CSS grid so the two chips
 * align vertically regardless of label width, and a top hairline so the
 * rows read as a list.
 */
const rowStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'minmax(5rem, max-content) 1fr',
  alignItems: 'center',
  gap: 'var(--nf-space-3)',
  paddingBlock: 'var(--nf-space-2)',
  borderBlockStart: '1px solid var(--nf-color-border)',
};

const labelStyle: CSSProperties = {
  margin: 0,
  fontSize: 'var(--nf-text-xs)',
  fontWeight: 500,
  color: 'var(--nf-color-fg-muted)',
  textTransform: 'uppercase',
  letterSpacing: '0.02em',
};

const valueStyle: CSSProperties = {
  margin: 0,
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--nf-space-1-5)',
};

export default function ReplayPanel({ taskId }: ReplayPanelProps): ReactElement {
  const { t } = useTranslation('constraints');
  const labelFor = useStateLabel();
  const q = useTaskReplayQuery(taskId);
  const drift = q.data != null && !q.data.equivalent;

  return (
    <section
      aria-label={t('replay.title')}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-3)',
        padding: 'var(--nf-space-3-5) var(--nf-space-4)',
        borderRadius: 'var(--nf-radius-md)',
        border: '1px solid var(--nf-color-border)',
        background: 'var(--nf-color-surface)',
      }}
    >
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--nf-space-3)',
        }}
      >
        <h3 style={{ margin: 0, fontSize: 'var(--nf-text-base)', fontWeight: 600 }}>
          {t('replay.title')}
        </h3>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => void q.refetch()}
          disabled={q.isFetching}
        >
          {t('replay.refresh')}
        </Button>
      </header>
      {q.isLoading ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('replay.loading')}</p>
      ) : null}
      {q.error ? (
        <p role="alert" style={{ margin: 0, color: 'var(--nf-color-danger)' }}>
          {t('replay.error')}
        </p>
      ) : null}
      {q.data ? (
        <dl style={{ margin: 0, display: 'flex', flexDirection: 'column' }}>
          <div style={rowStyle}>
            <dt style={labelStyle}>{t('replay.derived')}</dt>
            <dd style={valueStyle}>
              <Chip tone="neutral">{labelFor(q.data.derivedState)}</Chip>
            </dd>
          </div>
          <div style={rowStyle}>
            <dt style={labelStyle}>{t('replay.stored')}</dt>
            <dd style={valueStyle}>
              <Chip tone={drift ? 'warning' : 'neutral'}>{labelFor(q.data.stored)}</Chip>
            </dd>
          </div>
          <div style={rowStyle}>
            <dt style={labelStyle}>
              {q.data.equivalent ? t('replay.equivalent') : t('replay.drift')}
            </dt>
            <dd style={valueStyle} aria-live="polite">
              <Chip tone={q.data.equivalent ? 'success' : 'danger'}>
                <span aria-hidden="true">{q.data.equivalent ? '✓' : '⚠'}</span>
                <span>{q.data.equivalent ? t('replay.equivalent') : t('replay.drift')}</span>
              </Chip>
            </dd>
          </div>
        </dl>
      ) : null}
    </section>
  );
}

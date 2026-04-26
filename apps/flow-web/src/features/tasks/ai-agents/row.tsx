/**
 * AIAgentsRow — single line item in the AI activity table.
 *
 * Renders the kind badge, model id, latency / token / cost columns,
 * the invocation timestamp, and a status glyph. When the invocation
 * failed, an "Show details" disclosure button reveals the redacted
 * provider response (or a placeholder if the backend gave us
 * nothing).
 *
 * The row deliberately avoids `useMemo` / `useCallback` (per
 * @web rules — React Compiler handles memoization) and keeps all
 * derived values inline. Colour tones for the kind badge and status
 * dot are computed via plain switch helpers; both consume design
 * tokens via CSS, never raw hex.
 */

import { type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpochDateTime } from '../../../lib/format';
import type { TaskAiInvocation } from '../api';
import styles from './ai-agents.module.css';

type KindTone = 'accent' | 'success' | 'warning' | 'info' | 'neutral';
type StatusTone = 'ok' | 'warning' | 'danger';

type PurposeKey =
  | 'prioritySuggest'
  | 'stateInfer'
  | 'reminder'
  | 'digest'
  | 'duplicates'
  | 'agent'
  | 'other';

/**
 * Map a backend `purpose` string to a stable display key. The backend
 * emits free-form purposes (e.g. `propose_priority`, `agent_tick`); we
 * normalise into a small fixed vocabulary so i18n keys stay finite and
 * unknown purposes fall through to `other`.
 */
function purposeKey(purpose: string): PurposeKey {
  const p = purpose.toLowerCase();
  if (p.includes('priority')) return 'prioritySuggest';
  if (p.includes('state') || p.includes('infer')) return 'stateInfer';
  if (p.includes('reminder')) return 'reminder';
  if (p.includes('digest')) return 'digest';
  if (p.includes('duplicate') || p.includes('dedup')) return 'duplicates';
  if (p.includes('agent')) return 'agent';
  return 'other';
}

/**
 * Static map from purpose key to its translation key. Keeping this
 * inline rather than building the i18n key with template literals
 * keeps the lookup statically analysable (per @web conventions:
 * dynamic i18n keys are forbidden). Translation keys themselves use
 * snake_case to match the original spec.
 */
const KIND_LABEL_KEY: Record<PurposeKey, string> = {
  prioritySuggest: 'kind.priority_suggest',
  stateInfer: 'kind.state_infer',
  reminder: 'kind.reminder',
  digest: 'kind.digest',
  duplicates: 'kind.duplicates',
  agent: 'kind.agent',
  other: 'kind.other',
};

function kindTone(key: PurposeKey): KindTone {
  switch (key) {
    case 'prioritySuggest':
      return 'warning';
    case 'stateInfer':
      return 'accent';
    case 'reminder':
      return 'info';
    case 'digest':
      return 'success';
    case 'duplicates':
      return 'info';
    case 'agent':
      return 'accent';
    default:
      return 'neutral';
  }
}

function statusTone(status: string): StatusTone {
  const s = status.toLowerCase();
  if (s === 'ok' || s === 'success') return 'ok';
  if (s === 'blocked' || s === 'rate_limited' || s === 'pending') return 'warning';
  return 'danger';
}

export interface AIAgentsRowProps {
  invocation: TaskAiInvocation;
  locale: string;
}

export default function AIAgentsRow({ invocation, locale }: AIAgentsRowProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  const [showError, setShowError] = useState(false);
  const errorId = useId();

  const key = purposeKey(invocation.purpose);
  const tone = kindTone(key);
  const status = statusTone(invocation.status);
  const isFailure = status === 'danger';

  const tokensIn = invocation.tokensInput ?? 0;
  const tokensOut = invocation.tokensOutput ?? 0;
  const hasTokens = tokensIn > 0 || tokensOut > 0;

  const cost = invocation.costEstimate ?? '';
  const hasCost = cost.length > 0;

  const formattedAt = formatEpochDateTime(invocation.invokedAt, locale) ?? '';

  const errorDetail = invocation.errorCode ?? invocation.responseRedacted ?? '';

  return (
    <li className={styles.row}>
      <span className={styles.kindCell}>
        <span className={styles.kindBadge} data-tone={tone}>
          {t(KIND_LABEL_KEY[key])}
        </span>
      </span>
      <span className={styles.modelCell} title={invocation.model}>
        {invocation.model}
      </span>
      <span className={styles.numericCell}>
        {hasTokens ? t('tokens.format', { in: tokensIn, out: tokensOut }) : '—'}
      </span>
      <span className={styles.numericCell}>{hasCost ? t('cost.usd', { value: cost }) : '—'}</span>
      <span className={styles.timeCell}>{formattedAt}</span>
      <span className={styles.statusCell}>
        <span
          className={styles.statusGlyph}
          data-tone={status}
          aria-label={status === 'ok' ? t('status.ok') : t('status.error')}
          role="img"
        />
      </span>
      {isFailure ? (
        <div className={styles.errorRow}>
          <button
            type="button"
            className={styles.errorToggle}
            aria-expanded={showError}
            aria-controls={errorId}
            onClick={() => {
              setShowError((prev) => !prev);
            }}
          >
            {showError ? t('error.hide') : t('error.detail')}
          </button>
          {showError ? (
            <pre id={errorId} className={styles.errorDetail}>
              {errorDetail.length > 0 ? errorDetail : t('error.empty')}
            </pre>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

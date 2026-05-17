/**
 * RetroDraftRow — single row in the retro drafts queue.
 *
 * Layout (fixed visual height per feedback_morph_layout_stability):
 *   - Title (`<Link>` to the draft task detail page)
 *   - "Linked to: {source title}" link → source task detail
 *   - Description (line-clamp: 2)
 *   - Meta footer (created N ago · by AI agent / anonymous)
 *   - Action cluster ([Discard] [Accept]) — pinned to the inline-end
 *
 * Buttons emit semantic callbacks; the parent page owns mutation state
 * so two rows can be busy concurrently without coupling through this
 * component. `busy` greys out the row's actions while the optimistic
 * update is in flight (rollback restores them).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { Link } from '@tanstack/react-router';
import { type ReactElement, memo } from 'react';
import { useTranslation } from 'react-i18next';

import { formatTimeAgo } from '../archived/relative-time';

import type { RetroDraft } from './retro-drafts-api';
import styles from './retro-drafts.module.css';

export interface RetroDraftRowProps {
  draft: RetroDraft;
  /** Resolved i18n locale so the "N ago" string is locale-aware. */
  locale: string;
  /** True while any mutation is in flight for this row. */
  busy: boolean;
  onAccept: (taskPublicId: string) => void;
  onDiscard: (taskPublicId: string) => void;
}

function RetroDraftRowImpl({
  draft,
  locale,
  busy,
  onAccept,
  onDiscard,
}: RetroDraftRowProps): ReactElement {
  const { t } = useTranslation('common');
  const ago = formatTimeAgo(draft.createdAt, locale) ?? '';

  const metaText = draft.createdByAgentName
    ? t('tasks.retro.queue.created_by_ai', {
        agentName: draft.createdByAgentName,
        when: ago,
      })
    : t('tasks.retro.queue.created_anon', { when: ago });

  return (
    <Card as="article" className={styles.row} aria-busy={busy ? 'true' : undefined}>
      <div className={styles.rowTopRow}>
        <Link
          to="/tasks/$taskId"
          params={{ taskId: draft.taskPublicId }}
          className={styles.rowTitle}
          dir="auto"
        >
          {draft.title}
        </Link>
        <Link
          to="/tasks/$taskId"
          params={{ taskId: draft.sourceTask.publicId }}
          className={styles.rowLinkedTo}
          dir="auto"
        >
          <span aria-hidden="true" className={styles.rowLinkedToArrow}>
            {'→'}
          </span>
          {t('tasks.retro.queue.linked_original', {
            sourceTitle: draft.sourceTask.title,
          })}
        </Link>
      </div>

      <p className={styles.rowDescription} dir="auto">
        {draft.description ?? ''}
      </p>

      <p className={styles.rowMeta}>{metaText}</p>

      <div className={styles.rowActions}>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={busy}
          onClick={() => {
            onDiscard(draft.taskPublicId);
          }}
        >
          {t('tasks.retro.queue.discard')}
        </Button>
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={busy}
          onClick={() => {
            onAccept(draft.taskPublicId);
          }}
        >
          {t('tasks.retro.queue.accept')}
        </Button>
      </div>
    </Card>
  );
}

/**
 * Memoised so accepting / discarding one row does not re-render
 * unrelated rows in the queue. Equality on the `draft` reference is
 * fine because the parent owns the list and mutates immutably.
 */
const RetroDraftRow = memo(RetroDraftRowImpl);
RetroDraftRow.displayName = 'RetroDraftRow';

export default RetroDraftRow;

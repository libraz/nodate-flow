/**
 * RetroDraftsPage — `/workspaces/{wsId}/tasks/drafts`.
 *
 * The retro draft queue surfaces every retrospective task drafted by
 * the signal_judge Applier (Phase 6 / L2 of
 * docs/plan/release-8-signals-and-judge-loop.md). Each row carries:
 *
 *   - The draft task's title (linked to its detail page)
 *   - A "Linked to: {source title}" back-reference (linked to the
 *     source task's detail page)
 *   - The draft description (line-clamp:2, fixed row height)
 *   - Agent attribution + "N ago" stamp
 *   - Accept / Discard buttons
 *
 * Accept removes the `retro_of` dependency edge (the draft task itself
 * is already at `derived_state='open'`, so dropping the edge promotes
 * it out of the queue). Discard archives the task — reversible via
 * the existing archive surface. Both mutations are optimistic with a
 * full snapshot rollback on error and a success toast on settle.
 *
 * Pagination is offset-based with a fixed page size (matches the
 * backend's default of 20, hard cap of 50). The page suspends on the
 * initial query; the route-level Suspense boundary owns the skeleton.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { confirm } from '@nodate-flow/ui/primitives/confirm';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import RetroDraftRow from './retro-draft-row';
import styles from './retro-drafts.module.css';
import {
  RETRO_DRAFTS_PAGE_SIZE,
  useAcceptRetroDraft,
  useDiscardRetroDraft,
  useRetroDraftsQuery,
} from './retro-drafts-api';

interface RetroDraftsPageProps {
  workspaceId: string;
}

export default function RetroDraftsPage({ workspaceId }: RetroDraftsPageProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';

  const [offset, setOffset] = useState(0);
  const draftsQuery = useRetroDraftsQuery(workspaceId, offset, RETRO_DRAFTS_PAGE_SIZE);
  const acceptMut = useAcceptRetroDraft();
  const discardMut = useDiscardRetroDraft();

  // Track per-row busy state so two rows can be in-flight concurrently
  // without disabling each other. The set is keyed on taskPublicId.
  const [busyIds, setBusyIds] = useState<ReadonlySet<string>>(() => new Set());

  const markBusy = useCallback((taskPublicId: string, on: boolean): void => {
    setBusyIds((prev) => {
      const next = new Set(prev);
      if (on) next.add(taskPublicId);
      else next.delete(taskPublicId);
      return next;
    });
  }, []);

  const handleAccept = useCallback(
    async (taskPublicId: string): Promise<void> => {
      markBusy(taskPublicId, true);
      try {
        await acceptMut.mutateAsync({ workspaceId, taskPublicId });
        toaster.show({
          tone: 'success',
          message: t('tasks.retro.queue.accepted_toast'),
        });
      } catch {
        toaster.show({
          tone: 'danger',
          message: t('tasks.retro.queue.accept_error'),
        });
      } finally {
        markBusy(taskPublicId, false);
      }
    },
    [acceptMut, markBusy, t, workspaceId],
  );

  const handleDiscard = useCallback(
    async (taskPublicId: string): Promise<void> => {
      const ok = await confirm.ask({
        title: t('tasks.retro.queue.discard_confirm.title'),
        message: t('tasks.retro.queue.discard_confirm.body'),
        confirmLabel: t('tasks.retro.queue.discard_confirm.confirm'),
        cancelLabel: t('tasks.retro.queue.discard_confirm.cancel'),
        tone: 'danger',
      });
      if (!ok) return;
      markBusy(taskPublicId, true);
      try {
        await discardMut.mutateAsync({ workspaceId, taskPublicId });
        toaster.show({
          tone: 'success',
          message: t('tasks.retro.queue.discarded_toast'),
        });
      } catch {
        toaster.show({
          tone: 'danger',
          message: t('tasks.retro.queue.discard_error'),
        });
      } finally {
        markBusy(taskPublicId, false);
      }
    },
    [discardMut, markBusy, t, workspaceId],
  );

  const { drafts, total, limit } = draftsQuery.data;
  const page = Math.floor(offset / limit) + 1;
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const hasPrev = offset > 0;
  const hasNext = offset + drafts.length < total;

  const handlePrev = useCallback((): void => {
    setOffset((prev) => Math.max(0, prev - limit));
  }, [limit]);

  const handleNext = useCallback((): void => {
    setOffset((prev) => prev + limit);
  }, [limit]);

  let body: ReactElement;
  if (total === 0) {
    body = (
      <EmptyState
        icon={
          <svg
            aria-hidden="true"
            viewBox="0 0 96 96"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ inlineSize: '5rem', blockSize: '5rem' }}
          >
            <rect x="14" y="14" width="68" height="68" rx="6" />
            <path d="M28 36h40M28 48h40M28 60h24" />
            <path d="M62 60h12" style={{ stroke: 'var(--nf-color-accent)' }} strokeWidth={2} />
          </svg>
        }
        title={t('tasks.retro.queue.empty')}
      />
    );
  } else {
    body = (
      <div className={styles.list}>
        {drafts.map((draft) => (
          <RetroDraftRow
            key={draft.taskPublicId}
            draft={draft}
            locale={locale}
            busy={busyIds.has(draft.taskPublicId)}
            onAccept={(id) => {
              void handleAccept(id);
            }}
            onDiscard={(id) => {
              void handleDiscard(id);
            }}
          />
        ))}

        {totalPages > 1 ? (
          <nav className={styles.pagination} aria-label={t('tasks.retro.queue.title')}>
            <span className={styles.paginationStatus} aria-live="polite">
              {`${page} / ${totalPages}`}
            </span>
            <div className={styles.paginationControls}>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={!hasPrev}
                onClick={handlePrev}
              >
                {'‹'}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={!hasNext}
                onClick={handleNext}
              >
                {'›'}
              </Button>
            </div>
          </nav>
        ) : null}
      </div>
    );
  }

  return (
    <main className={styles.page} aria-labelledby="retro-drafts-title">
      <header className={styles.header}>
        <h1 id="retro-drafts-title" className={styles.title}>
          {t('tasks.retro.queue.title')}
        </h1>
        <p className={styles.subtitle}>{t('tasks.retro.queue.description')}</p>
      </header>
      {body}
    </main>
  );
}

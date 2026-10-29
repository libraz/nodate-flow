/**
 * PriorityPage — `/workspaces/{wsId}/insights/priority`.
 *
 * Surfaces the deterministic AI priority suggestions returned by
 * `GET /workspaces/{wsId}/ai/priority-suggestions` as a stack of
 * before/after cards. There is no dedicated apply / dismiss endpoint:
 *
 *   - **Apply** mutates the task via `useUpdateTask`
 *     (`PATCH /tasks/{id}` with `{ priority }`). The card is hidden
 *     optimistically via a local `applied` set so the KPI and subtitle
 *     update before the `aiPriorityKeys.all` refetch returns. On success
 *     we toast with an Undo that PATCHes the task back to the previous
 *     priority and restores the row. On error the row comes back.
 *
 *   - **Dismiss** is local-only: we record the taskId in localStorage
 *     (see `./dismiss-store.ts`) and filter the list. The toast Undo
 *     un-records it.
 *
 * A card leaving the list for either reason collapses out through
 * `usePresenceList`, and one restored by Undo or a failed apply enters
 * again.
 *
 * Empty states distinguish "no open tasks at all" (`total === 0`) from
 * "all open tasks already match the recommended priority"
 * (`total > 0 && suggestions === 0`).
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { type ToastOptions, toaster } from '@nodate-flow/ui/primitives/toast';
import motionStyles from '@nodate-flow/ui/styles/list-motion.module.css';
import { useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { type TaskPriority, useUpdateTask } from '../tasks/api';
import { aiPriorityKeys, type PrioritySuggestion, useAiPrioritySuggestionsQuery } from './api';
import { useDismissedSuggestions } from './dismiss-store';
import PriorityError from './priority-error';
import styles from './priority-page.module.css';
import SuggestionCard from './suggestion-card';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/insights/priority');

/** Static i18n keys per priority bucket — keeps i18n keys static. */
function priorityLabelKey(p: TaskPriority): string {
  switch (p) {
    case 0:
      return 'priority.p0';
    case 1:
      return 'priority.p1';
    case 2:
      return 'priority.p2';
    case 3:
      return 'priority.p3';
    case 4:
      return 'priority.p4';
  }
}

/** Clamp a numeric priority to the legal {@link TaskPriority} range. */
function clampPriority(value: number): TaskPriority {
  if (value <= 0) return 0;
  if (value >= 4) return 4;
  return Math.round(value) as TaskPriority;
}

type ToastSpec = Pick<ToastOptions, 'message' | 'tone' | 'action'>;

/**
 * Public wrapper that scopes a local ErrorBoundary around the suspense
 * query so a failed initial GET renders the page's own `PriorityError`
 * fallback instead of escalating past the workspace `errorComponent`
 * (which only catches `WS.WORKSPACE.NOT_FOUND`) into the root
 * FatalFallback. Resetting the boundary invalidates the query so the
 * Retry button refetches transparently.
 */
export default function PriorityPage(): ReactElement {
  const queryClient = useQueryClient();
  const { id: workspaceId } = routeApi.useParams();
  return (
    <ErrorBoundary
      FallbackComponent={PriorityError}
      onReset={() => {
        queryClient.invalidateQueries({ queryKey: aiPriorityKeys.list(workspaceId) });
      }}
    >
      <Suspense fallback={<PrioritySkeleton />}>
        <PriorityImpl />
      </Suspense>
    </ErrorBoundary>
  );
}

const SKELETON_CARDS = ['a', 'b', 'c'] as const;

/** Mirrors the header and the first cards so the page does not jump on load. */
function PrioritySkeleton(): ReactElement {
  return (
    <section className={styles.page} aria-busy="true">
      <header className={styles.header}>
        <Skeleton className={styles.skeletonTitle} />
        <Skeleton className={styles.skeletonLine} />
        <Skeleton className={styles.skeletonLine} />
      </header>
      <div className={styles.list}>
        {SKELETON_CARDS.map((key) => (
          <Skeleton key={key} className={styles.skeletonCard} />
        ))}
      </div>
    </section>
  );
}

function PriorityImpl(): ReactElement {
  const { t } = useTranslation('aiPriority');
  const { id: workspaceId } = routeApi.useParams();
  const { data, refetch, isFetching } = useAiPrioritySuggestionsQuery(workspaceId);
  const updateTask = useUpdateTask();
  const { dismissed, dismiss, undismiss } = useDismissedSuggestions(workspaceId);
  const qc = useQueryClient();

  // Per-row UI state. `busy` keeps both Apply / Dismiss disabled until
  // the underlying mutation settles; `applied` hides the row from the
  // visible list optimistically until the suggestions query refetches.
  // Both live for the lifetime of one page mount.
  const [busy, setBusy] = useState<readonly string[]>([]);
  const [applied, setApplied] = useState<readonly string[]>([]);

  const dismissedSet = new Set(dismissed);
  const busySet = new Set(busy);
  const appliedSet = new Set(applied);

  const visible = data.suggestions.filter(
    (s) => !dismissedSet.has(s.taskId) && !appliedSet.has(s.taskId),
  );
  const { entries, onExited } = usePresenceList(visible, (s) => s.taskId);
  const staggerStyle = useFirstLoadStagger();
  const totalEvaluated = data.total;
  const shown = visible.length;

  function showToast(spec: ToastSpec): void {
    toaster.show(spec);
  }

  /** Run a PATCH /tasks/{id} for a single priority change. */
  function patchPriority(taskId: string, priority: TaskPriority): Promise<void> {
    return updateTask.mutateAsync({ id: taskId, patch: { priority } }).then(() => undefined);
  }

  /** Refresh the suggestions list so KPIs and rows reconcile with the server. */
  function invalidateSuggestions(): void {
    void qc.invalidateQueries({ queryKey: aiPriorityKeys.all });
  }

  /** True while the row cannot take another action: in flight, or already gone. */
  function isSettled(taskId: string): boolean {
    return busySet.has(taskId) || appliedSet.has(taskId) || dismissedSet.has(taskId);
  }

  function handleApply(suggestion: PrioritySuggestion): void {
    if (isSettled(suggestion.taskId)) return;
    const previous = clampPriority(suggestion.currentPriority);
    const next = clampPriority(suggestion.suggestedPriority);

    // Optimistic: the row leaves the visible list while the PATCH runs.
    setBusy((prev) => [...prev, suggestion.taskId]);
    setApplied((prev) => [...prev, suggestion.taskId]);

    patchPriority(suggestion.taskId, next)
      .then(() => {
        // The next refetch reconciles the canonical list (the suggestion is
        // no longer eligible because current === suggested now).
        invalidateSuggestions();
        showToast({
          tone: 'success',
          message: t('toast.applied', { priority: t(priorityLabelKey(next)) }),
          action: {
            label: t('common:common.undo'),
            onAction: () => {
              // Undo: restore the previous priority and bring the row back.
              // Busy while the PATCH runs so the row cannot be double-actioned.
              setBusy((prev) => [...prev, suggestion.taskId]);
              patchPriority(suggestion.taskId, previous)
                .then(() => {
                  setApplied((prev) => prev.filter((id) => id !== suggestion.taskId));
                  invalidateSuggestions();
                  showToast({ tone: 'info', message: t('toast.undone') });
                })
                .catch((err: unknown) => {
                  showToast({
                    tone: 'danger',
                    message: formatApiError(err, t, 'toast.applyFailed'),
                  });
                })
                .finally(() => {
                  setBusy((prev) => prev.filter((id) => id !== suggestion.taskId));
                });
            },
          },
        });
      })
      .catch((err: unknown) => {
        // Roll back: the row returns so the user can retry.
        setApplied((prev) => prev.filter((id) => id !== suggestion.taskId));
        showToast({ tone: 'danger', message: formatApiError(err, t, 'toast.applyFailed') });
      })
      .finally(() => {
        setBusy((prev) => prev.filter((id) => id !== suggestion.taskId));
      });
  }

  function handleDismiss(suggestion: PrioritySuggestion): void {
    if (isSettled(suggestion.taskId)) return;
    dismiss(suggestion.taskId);
    showToast({
      tone: 'info',
      message: t('toast.dismissed'),
      action: {
        label: t('common:common.undo'),
        onAction: () => {
          undismiss(suggestion.taskId);
          showToast({ tone: 'info', message: t('toast.undone') });
        },
      },
    });
  }

  let content: ReactElement;
  if (totalEvaluated === 0) {
    content = (
      <Card>
        <EmptyState titleAs="p" title={t('empty.noTasks')} />
      </Card>
    );
  } else if (entries.length === 0) {
    content = (
      <Card className={motionStyles.enter}>
        <EmptyState titleAs="p" title={t('empty.allOptimised')} />
      </Card>
    );
  } else {
    content = (
      <ul className={styles.list}>
        {entries.map((entry, index) => (
          <PresenceItem
            key={entry.key}
            as="li"
            present={entry.present}
            onExited={() => {
              onExited(entry.key);
            }}
            className={motionStyles.enter}
            style={staggerStyle(index)}
          >
            <SuggestionCard
              suggestion={entry.item}
              busy={busySet.has(entry.key)}
              onApply={handleApply}
              onDismiss={handleDismiss}
            />
          </PresenceItem>
        ))}
      </ul>
    );
  }

  return (
    <section className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('page.title')}</h1>
        <p className={styles.subtitle}>{t('page.subtitle', { shown, total: totalEvaluated })}</p>
        <div className={styles.kpiRow}>
          <span className={styles.kpi}>
            <span className={styles.kpiValue}>{shown}</span>
            <span className={styles.kpiLabel}>{t('kpi.suggestions')}</span>
          </span>
          <span className={styles.kpiSeparator} aria-hidden>
            /
          </span>
          <span className={styles.kpi}>
            <span className={styles.kpiValue}>{totalEvaluated}</span>
            <span className={styles.kpiLabel}>{t('kpi.evaluated')}</span>
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              void refetch();
            }}
            disabled={isFetching}
            aria-busy={isFetching || undefined}
            className={styles.refreshButton}
          >
            {t('action.refresh')}
          </Button>
        </div>
      </header>
      {content}
    </section>
  );
}

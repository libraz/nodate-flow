/**
 * PriorityPage — `/workspaces/{wsId}/insights/priority`.
 *
 * Surfaces the deterministic AI priority suggestions returned by
 * `GET /workspaces/{wsId}/ai/priority-suggestions` as a stack of
 * before/after cards. There is no dedicated apply / dismiss endpoint:
 *
 *   - **Apply** mutates the task via `useUpdateTask`
 *     (`PATCH /tasks/{id}` with `{ priority }`). The card fades out
 *     optimistically and is hidden from the visible list via a local
 *     `applied` set so the KPI and subtitle update before the
 *     `aiPriorityKeys.all` refetch returns. On success we toast with
 *     an Undo that PATCHes the task back to the previous priority and
 *     restores the row. On error the optimistic state rolls back.
 *
 *   - **Dismiss** is local-only: we record the taskId in localStorage
 *     (see `./dismiss-store.ts`) and filter the list. The toast Undo
 *     un-records it.
 *
 * Empty states distinguish "no open tasks at all" (`total === 0`) from
 * "all open tasks already match the recommended priority"
 * (`total > 0 && suggestions === 0`).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
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

interface ToastSpec {
  message: React.ReactNode;
  tone: 'info' | 'success' | 'warning' | 'danger';
}

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
      <PriorityImpl />
    </ErrorBoundary>
  );
}

function PriorityImpl(): ReactElement {
  const { t } = useTranslation('aiPriority');
  const { id: workspaceId } = routeApi.useParams();
  const { data, refetch, isFetching } = useAiPrioritySuggestionsQuery(workspaceId);
  const updateTask = useUpdateTask();
  const { dismissed, dismiss, undismiss } = useDismissedSuggestions(workspaceId);
  const qc = useQueryClient();

  // Per-row UI state. `exiting` drives the fade-out animation; `busy`
  // keeps both Apply / Dismiss disabled until the underlying mutation
  // settles; `applied` hides the row from the visible list optimistically
  // until the suggestions query refetches. All three live for the
  // lifetime of one page mount.
  const [exiting, setExiting] = useState<readonly string[]>([]);
  const [busy, setBusy] = useState<readonly string[]>([]);
  const [applied, setApplied] = useState<readonly string[]>([]);

  const dismissedSet = new Set(dismissed);
  const exitingSet = new Set(exiting);
  const busySet = new Set(busy);
  const appliedSet = new Set(applied);

  const visible = data.suggestions.filter(
    (s) => !dismissedSet.has(s.taskId) && !appliedSet.has(s.taskId),
  );
  const totalEvaluated = data.total;
  const shown = visible.length;

  function showToast(spec: ToastSpec): void {
    toaster.show({ message: spec.message, tone: spec.tone });
  }

  /** Run a PATCH /tasks/{id} for a single priority change. */
  function patchPriority(taskId: string, priority: TaskPriority): Promise<void> {
    return updateTask.mutateAsync({ id: taskId, patch: { priority } }).then(() => undefined);
  }

  /** Refresh the suggestions list so KPIs and rows reconcile with the server. */
  function invalidateSuggestions(): void {
    void qc.invalidateQueries({ queryKey: aiPriorityKeys.all });
  }

  function handleApply(suggestion: PrioritySuggestion): void {
    if (
      busySet.has(suggestion.taskId) ||
      exitingSet.has(suggestion.taskId) ||
      appliedSet.has(suggestion.taskId)
    ) {
      return;
    }
    const previous = clampPriority(suggestion.currentPriority);
    const next = clampPriority(suggestion.suggestedPriority);

    // Optimistic: fade the row out and hide it from the visible list.
    setBusy((prev) => [...prev, suggestion.taskId]);
    setExiting((prev) => [...prev, suggestion.taskId]);

    patchPriority(suggestion.taskId, next)
      .then(() => {
        // Commit the optimistic removal: the row is gone from `visible`
        // via `applied`, and the next refetch will reconcile the canonical
        // list (the suggestion is no longer eligible because current ===
        // suggested now). Clearing `exiting` is a no-op visually because
        // the row is already filtered out, but it keeps state tidy in case
        // Undo restores it.
        setApplied((prev) => [...prev, suggestion.taskId]);
        setExiting((prev) => prev.filter((id) => id !== suggestion.taskId));
        invalidateSuggestions();
        showToast({
          tone: 'success',
          message: (
            <span className={styles.toastBody}>
              <span>{t('toast.applied', { priority: t(priorityLabelKey(next)) })}</span>
              <button
                type="button"
                className={styles.toastUndo}
                onClick={() => {
                  // Undo: restore the previous priority and bring the row
                  // back into the visible list. Mark busy so the user can't
                  // double-click while the PATCH is in flight.
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
                }}
              >
                {t('toast.undo')}
              </button>
            </span>
          ),
        });
      })
      .catch((err: unknown) => {
        // Roll back the fade; the row reappears so the user can retry.
        setExiting((prev) => prev.filter((id) => id !== suggestion.taskId));
        showToast({ tone: 'danger', message: formatApiError(err, t, 'toast.applyFailed') });
      })
      .finally(() => {
        setBusy((prev) => prev.filter((id) => id !== suggestion.taskId));
      });
  }

  function handleDismiss(suggestion: PrioritySuggestion): void {
    if (
      busySet.has(suggestion.taskId) ||
      exitingSet.has(suggestion.taskId) ||
      appliedSet.has(suggestion.taskId)
    ) {
      return;
    }
    setExiting((prev) => [...prev, suggestion.taskId]);
    // Persist after the fade-out frame so the user sees the animation.
    window.setTimeout(() => {
      dismiss(suggestion.taskId);
      setExiting((prev) => prev.filter((id) => id !== suggestion.taskId));
    }, 200);
    showToast({
      tone: 'info',
      message: (
        <span className={styles.toastBody}>
          <span>{t('toast.dismissed')}</span>
          <button
            type="button"
            className={styles.toastUndo}
            onClick={() => {
              undismiss(suggestion.taskId);
              showToast({ tone: 'info', message: t('toast.undone') });
            }}
          >
            {t('toast.undo')}
          </button>
        </span>
      ),
    });
  }

  let content: ReactElement;
  if (totalEvaluated === 0) {
    content = (
      <Card>
        <p className={styles.empty}>{t('empty.noTasks')}</p>
      </Card>
    );
  } else if (visible.length === 0) {
    content = (
      <Card>
        <p className={styles.empty}>{t('empty.allOptimised')}</p>
      </Card>
    );
  } else {
    content = (
      <ul className={styles.list}>
        {visible.map((s) => (
          <SuggestionCard
            key={s.taskId}
            suggestion={s}
            busy={busySet.has(s.taskId)}
            exiting={exitingSet.has(s.taskId)}
            onApply={handleApply}
            onDismiss={handleDismiss}
          />
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

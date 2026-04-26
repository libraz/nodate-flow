/**
 * PriorityPage — `/workspaces/{wsId}/insights/priority`.
 *
 * Surfaces the deterministic AI priority suggestions returned by
 * `GET /workspaces/{wsId}/ai/priority-suggestions` as a stack of
 * before/after cards. There is no dedicated apply / dismiss endpoint:
 *
 *   - **Apply** mutates the task via `useUpdateTask`
 *     (`PATCH /tasks/{id}` with `{ priority }`) optimistically — the
 *     suggestion fades out, then the row is removed once the response
 *     lands. Failures roll the fade back and surface a toast. The success
 *     toast carries an Undo that restores the previous priority via the
 *     same mutation.
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
import { getRouteApi } from '@tanstack/react-router';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { type TaskPriority, useUpdateTask } from '../tasks/api';
import { type PrioritySuggestion, useAiPrioritySuggestionsQuery } from './api';
import { useDismissedSuggestions } from './dismiss-store';
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

export default function PriorityPage(): ReactElement {
  const { t } = useTranslation('aiPriority');
  const { id: workspaceId } = routeApi.useParams();
  const { data, refetch, isFetching } = useAiPrioritySuggestionsQuery(workspaceId);
  const updateTask = useUpdateTask();
  const { dismissed, dismiss, undismiss } = useDismissedSuggestions(workspaceId);

  // Per-row UI state. `exiting` drives the fade-out animation; `busy`
  // keeps both Apply / Dismiss disabled until the underlying mutation
  // settles. Stored as plain Sets in component state — every value lives
  // for the lifetime of one page mount.
  const [exiting, setExiting] = useState<readonly string[]>([]);
  const [busy, setBusy] = useState<readonly string[]>([]);

  const dismissedSet = new Set(dismissed);
  const exitingSet = new Set(exiting);
  const busySet = new Set(busy);

  const visible = data.suggestions.filter((s) => !dismissedSet.has(s.taskId));
  const totalEvaluated = data.total;
  const shown = visible.length;

  function showToast(spec: ToastSpec): void {
    toaster.show({ message: spec.message, tone: spec.tone });
  }

  function applyPriority(taskId: string, priority: TaskPriority): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      updateTask.mutate(
        { id: taskId, patch: { priority } },
        {
          onSuccess: () => resolve(),
          onError: (err) => reject(err),
        },
      );
    });
  }

  function handleApply(suggestion: PrioritySuggestion): void {
    if (busySet.has(suggestion.taskId) || exitingSet.has(suggestion.taskId)) return;
    const previous = clampPriority(suggestion.currentPriority);
    const next = clampPriority(suggestion.suggestedPriority);

    // Optimistic fade — the row hides immediately; we re-show it on error.
    setBusy((prev) => [...prev, suggestion.taskId]);
    setExiting((prev) => [...prev, suggestion.taskId]);

    applyPriority(suggestion.taskId, next)
      .then(() => {
        showToast({
          tone: 'success',
          message: (
            <span className={styles.toastBody}>
              <span>{t('toast.applied', { priority: t(priorityLabelKey(next)) })}</span>
              <button
                type="button"
                className={styles.toastUndo}
                onClick={() => {
                  // Undo: restore the previous priority. Fire-and-forget;
                  // the optimistic fade is already gone so we just toast.
                  applyPriority(suggestion.taskId, previous)
                    .then(() => {
                      showToast({ tone: 'info', message: t('toast.undone') });
                    })
                    .catch((err: unknown) => {
                      showToast({
                        tone: 'danger',
                        message: formatApiError(err, t, 'toast.applyFailed'),
                      });
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
    if (busySet.has(suggestion.taskId) || exitingSet.has(suggestion.taskId)) return;
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

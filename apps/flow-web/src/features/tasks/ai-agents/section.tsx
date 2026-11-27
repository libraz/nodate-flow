/**
 * AIAgentsSection — the "AI activity" list of the task detail page,
 * shown as the body of the activity tabs (the tab is its heading).
 * Lists every recorded invocation against the task
 * (priority suggestions, state inferences, agent ticks, …) with
 * one row per call — kind badge, model id, token / cost columns,
 * timestamp, success/failure dot. Failures expose a "Show details"
 * disclosure that reveals the redacted provider response.
 *
 * Data is loaded with `useTaskAiInvocationsQuery`
 * (`useSuspenseQuery` under the hood) — the parent route mounts the
 * section inside a `<Suspense fallback={<AIAgentsSkeleton />}>`
 * boundary so the skeleton matches the eventual row rhythm.
 *
 * The component scopes a local ErrorBoundary around the suspense query
 * so a failed initial GET renders `AIAgentsError` in place of the
 * section body — escalations to the route-level FatalFallback are
 * reserved for genuinely fatal failures elsewhere on the page.
 */

import { useQueryClient } from '@tanstack/react-query';
import type { ReactElement } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { tasksKeys, useTaskAiInvocationsQuery } from '../api';
import styles from './ai-agents.module.css';
import AIAgentsEmpty from './empty';
import AIAgentsError from './error';
import AIAgentsRow from './row';

export interface AIAgentsSectionProps {
  taskId: string;
  locale: string;
}

/**
 * Public wrapper that scopes a local ErrorBoundary around the suspense
 * query so a failed initial GET renders the section's own
 * `AIAgentsError` fallback instead of escalating to the route-level
 * FatalFallback. Resetting the boundary invalidates the query so the
 * Retry button refetches transparently.
 */
export default function AIAgentsSection(props: AIAgentsSectionProps): ReactElement {
  const queryClient = useQueryClient();
  return (
    <ErrorBoundary
      FallbackComponent={AIAgentsError}
      onReset={() => {
        queryClient.invalidateQueries({ queryKey: tasksKeys.aiInvocations(props.taskId) });
      }}
    >
      <AIAgentsImpl {...props} />
    </ErrorBoundary>
  );
}

function AIAgentsImpl({ taskId, locale }: AIAgentsSectionProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: invocations } = useTaskAiInvocationsQuery(taskId);

  return (
    <section className={styles.section} aria-label={t('section.title')}>
      <div className={styles.body}>
        {invocations.length === 0 ? (
          <AIAgentsEmpty />
        ) : (
          <ul className={styles.list}>
            {invocations.map((invocation) => (
              <AIAgentsRow key={invocation.id} invocation={invocation} locale={locale} />
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

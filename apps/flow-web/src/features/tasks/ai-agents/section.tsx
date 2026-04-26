/**
 * AIAgentsSection — collapsible "AI activity" sub-section on the
 * task detail page.
 *
 * Sits alongside `LinkedEventsSection` / `DependenciesSection` /
 * `TaskAttachments` etc. as a peer of the main column, NOT as a
 * route-level tab. Lists every recorded invocation against the task
 * (priority suggestions, state inferences, agent ticks, …) with
 * one row per call — kind badge, model id, token / cost columns,
 * timestamp, success/failure dot. Failures expose a "Show details"
 * disclosure that reveals the redacted provider response.
 *
 * Disclosure state is persisted in localStorage via the same
 * `useCollapsibleState` hook used by the linked-events section, so
 * the user's preference travels across reloads. The default is open
 * when there are 1..N invocations (so the activity is visible) and
 * collapsed when the list grows long enough to push other sections
 * off-screen.
 *
 * Data is loaded with `useTaskAiInvocationsQuery`
 * (`useSuspenseQuery` under the hood) — the parent route mounts the
 * section inside a `<Suspense fallback={<AIAgentsSkeleton />}>`
 * boundary so the skeleton matches the eventual row rhythm.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useTaskAiInvocationsQuery } from '../api';
import { useCollapsibleState } from '../links/hooks/use-collapsible-state';
import styles from './ai-agents.module.css';
import AIAgentsEmpty from './empty';
import AIAgentsRow from './row';

const COLLAPSE_KEY = 'ai-agents';
const COLLAPSE_THRESHOLD = 8;

export interface AIAgentsSectionProps {
  taskId: string;
  locale: string;
}

export default function AIAgentsSection({ taskId, locale }: AIAgentsSectionProps): ReactElement {
  const { t } = useTranslation('aiAgents');
  const { data: invocations } = useTaskAiInvocationsQuery(taskId);

  const total = invocations.length;
  const initialCollapsed = total > COLLAPSE_THRESHOLD;
  const { collapsed, toggle } = useCollapsibleState(COLLAPSE_KEY, initialCollapsed);

  const titleId = `${COLLAPSE_KEY}-title`;
  const bodyId = `${COLLAPSE_KEY}-body`;
  const isEmpty = total === 0;

  return (
    <section className={styles.section} aria-labelledby={titleId}>
      <div className={styles.sectionHeader}>
        <button
          type="button"
          className={styles.disclosure}
          aria-expanded={!collapsed}
          aria-controls={bodyId}
          onClick={toggle}
        >
          <svg
            className={styles.caret}
            data-open={collapsed ? undefined : 'true'}
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M6 4l4 4-4 4" />
          </svg>
          <h3 id={titleId} className={styles.title}>
            {t('section.title')}
          </h3>
          <span className={styles.count}>{t('section.count', { count: total })}</span>
        </button>
      </div>

      <div id={bodyId} className={styles.body} hidden={collapsed}>
        {isEmpty ? (
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

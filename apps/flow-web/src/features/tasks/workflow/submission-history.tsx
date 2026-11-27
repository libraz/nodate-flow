import Badge from '@nodate-flow/ui/primitives/badge';
import type { Zone } from '@nodate-flow/ui/time';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { formatEpochDateTime } from '../../../lib/format';
import { safeSourceUrl } from '../../inbox/source-intake/safe-source-url';
import type { WorkflowSnapshot } from './api';
import css from './task-workflow-panel.module.css';

interface SubmissionHistoryProps {
  submissions: NonNullable<WorkflowSnapshot['recentSubmissions']>;
  currentRevision: number;
  locale: string;
  zone: Zone;
}

/** Collapsible list of earlier submissions with their review decisions. */
export default function SubmissionHistory({
  submissions,
  currentRevision,
  locale,
  zone,
}: SubmissionHistoryProps): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section className={css.section} aria-label={t('taskWorkflow.submissionHistory')}>
      <details className={css.history}>
        <summary className={css.historySummary}>
          <span>{t('taskWorkflow.submissionHistory')}</span>
          <Badge tone="neutral" className={css.numeric}>
            {submissions.length}
          </Badge>
        </summary>
        <ol className={css.list}>
          {submissions.map((historySubmission) => {
            const decision = historySubmission.decision;
            const isStaleApproval =
              decision?.decision === 'approved' && historySubmission.revision !== currentRevision;
            const createdAt = new Date(historySubmission.createdAt * 1000);
            const decisionLabel =
              decision?.decision === 'approved'
                ? t('taskWorkflow.approved')
                : decision?.decision === 'rejected'
                  ? t('taskWorkflow.rejected')
                  : decision?.decision
                    ? decision.decision
                    : t('taskWorkflow.pendingReview');
            const decisionTone =
              decision?.decision === 'approved'
                ? isStaleApproval
                  ? 'neutral'
                  : 'success'
                : decision?.decision === 'rejected'
                  ? 'warning'
                  : 'neutral';
            const acceptanceCriteria = historySubmission.acceptanceCriteria ?? [];

            return (
              <li key={historySubmission.id} className={`${css.item} ${css.historyItem}`}>
                <div className={css.itemBody}>
                  <div className={css.summary}>
                    <strong className={css.numeric}>
                      {t('taskWorkflow.historyRevision', {
                        revision: historySubmission.revision,
                      })}
                    </strong>
                    <time
                      className={`${css.muted} ${css.numeric}`}
                      dateTime={createdAt.toISOString()}
                    >
                      {formatEpochDateTime(historySubmission.createdAt, locale, zone)}
                    </time>
                  </div>
                  <div className={css.summary}>
                    <Badge tone={decisionTone}>{decisionLabel}</Badge>
                    {isStaleApproval ? (
                      <Badge tone="warning">{t('taskWorkflow.staleHistoryApproval')}</Badge>
                    ) : null}
                  </div>
                  {decision?.reason ? (
                    <p className={css.muted}>
                      <strong className={css.label}>
                        {t('taskWorkflow.previousReviewFeedback')}
                      </strong>
                      {decision.reason}
                    </p>
                  ) : null}
                  {acceptanceCriteria.length > 0 ? (
                    <div
                      className={css.itemBody}
                      role="group"
                      aria-label={t('taskWorkflow.acceptanceCriteria')}
                    >
                      <strong>{t('taskWorkflow.acceptanceCriteria')}</strong>
                      <ul className={css.list}>
                        {acceptanceCriteria.map((criterion) => (
                          <li key={`${historySubmission.id}-${criterion}`}>{criterion}</li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {(historySubmission.evidenceUrls ?? []).length > 0 ? (
                    <div className={css.itemBody}>
                      <strong>{t('taskWorkflow.evidence')}</strong>
                      <ul className={css.list}>
                        {(historySubmission.evidenceUrls ?? []).map((value) => {
                          const url = safeSourceUrl(value);
                          return (
                            <li key={value}>
                              {url ? (
                                <a
                                  className={css.safeLink}
                                  href={url}
                                  target="_blank"
                                  rel="noreferrer"
                                >
                                  {url}
                                </a>
                              ) : (
                                <span>{value}</span>
                              )}
                            </li>
                          );
                        })}
                      </ul>
                    </div>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ol>
      </details>
    </section>
  );
}

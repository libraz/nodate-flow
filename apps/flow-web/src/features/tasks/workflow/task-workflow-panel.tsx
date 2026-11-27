import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import FormField from '@nodate-flow/ui/primitives/form-field';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { QueryErrorResetBoundary, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ChevronDown, ChevronUp, UserCheck } from 'lucide-react';
import { type FormEvent, type ReactElement, Suspense, useEffect, useRef, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import FormActions from '../../../components/form-actions';
import { formatApiError } from '../../../lib/api-error';
import { confirmAction } from '../../../lib/confirm-action';
import { endOfDayUnix } from '../../../lib/date-utils';
import { formatDateOnly, formatEpoch, formatEpochDateTime } from '../../../lib/format';
import { onSubmitShortcut } from '../../../lib/submit-shortcut';
import { useEffectiveZone } from '../../../lib/use-effective-timezone';
import { useWeekStart } from '../../../lib/use-week-start';
import { type ConstraintOutcome, useEvaluateConstraints } from '../../constraints/api';
import { KIND_LABEL_KEY } from '../../constraints/constraint-editor';
import { safeSourceUrl } from '../../inbox/source-intake/safe-source-url';
import { useWorkspaceMembersQuery } from '../../workspaces/api';
import { type TaskActor, useListAttachments, useTaskActorsQuery, useTaskQuery } from '../api';
import TaskActionButton from '../task-action-button';
import TaskSectionHeading from '../task-section-heading';
import {
  useCreateHandoff,
  useCreateSubmission,
  useDecideHandoff,
  useDecideSubmission,
  useSetCompletionPolicy,
  useWorkflowQuery,
  type WorkflowHandoff,
  workflowKeys,
} from './api';
import css from './task-workflow-panel.module.css';

const HANDOFF_ROLES = ['assignee', 'reviewer', 'approver'] as const;
type HandoffRole = (typeof HANDOFF_ROLES)[number];

const HANDOFF_ROLE_KEY: Record<HandoffRole, string> = {
  assignee: 'taskWorkflow.roles.assignee',
  reviewer: 'taskWorkflow.roles.reviewer',
  approver: 'taskWorkflow.roles.approver',
};

type HandoffStatus = 'pending' | 'accepted' | 'declined' | 'expired' | 'cancelled';

const HANDOFF_STATUS_KEY: Record<HandoffStatus, string> = {
  pending: 'taskWorkflow.handoffStatus.pending',
  accepted: 'taskWorkflow.handoffStatus.accepted',
  declined: 'taskWorkflow.handoffStatus.declined',
  expired: 'taskWorkflow.handoffStatus.expired',
  cancelled: 'taskWorkflow.handoffStatus.cancelled',
};

const HANDOFF_STATUS_TONE: Record<HandoffStatus, BadgeTone> = {
  pending: 'info',
  accepted: 'success',
  declined: 'neutral',
  expired: 'neutral',
  cancelled: 'neutral',
};

function isHandoffRole(value: string): value is HandoffRole {
  return value in HANDOFF_ROLE_KEY;
}

function isHandoffStatus(value: string): value is HandoffStatus {
  return value in HANDOFF_STATUS_KEY;
}

/** The control an action error belongs to; the message renders beside it. */
type ActionScope =
  | 'policy'
  | 'checks'
  | 'submit'
  | 'reason'
  | 'decision'
  | 'handoff'
  | 'handoffDecision';

export interface TaskWorkflowPanelProps {
  taskId: string;
  workspaceId: string;
  projectId: string;
}

function splitLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function getRoleActors(actors: TaskActor[]): TaskActor[] {
  return actors.filter((actor) => actor.role === 'reviewer' || actor.role === 'approver');
}

/** Stands in for the compact button most tasks settle on: manual completion, nothing in review. */
function LoadingCard(): ReactElement {
  return (
    <div data-compact="">
      <Skeleton
        style={{ blockSize: 'var(--nf-space-8)', inlineSize: 'calc(var(--nf-space-8) * 4)' }}
      />
    </div>
  );
}

function ErrorCard({
  projectId,
  workspaceId,
  onRetry,
}: {
  projectId: string;
  workspaceId: string;
  onRetry: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section className={css.card}>
      <TaskSectionHeading title={t('taskWorkflow.title')} />
      <p className={css.error} role="alert">
        {t('taskWorkflow.loadFailed')}
      </p>
      <div className={css.row}>
        <Button type="button" size="sm" onClick={onRetry}>
          {t('taskWorkflow.retry')}
        </Button>
        <Link
          to="/workspaces/$id/projects/$projectId/tasks"
          params={{ id: workspaceId, projectId }}
        >
          {t('taskWorkflow.backToTasks')}
        </Link>
      </div>
    </section>
  );
}

function TaskWorkflowPanelFallback({
  error,
  resetErrorBoundary,
  projectId,
  workspaceId,
}: {
  error: unknown;
  resetErrorBoundary: () => void;
  projectId: string;
  workspaceId: string;
}): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section className={css.card}>
      <TaskSectionHeading title={t('taskWorkflow.title')} />
      <p className={css.error} role="alert">
        {formatApiError(error, t, 'taskWorkflow.loadFailed')}
      </p>
      <div className={css.row}>
        <Button type="button" size="sm" onClick={resetErrorBoundary}>
          {t('taskWorkflow.retry')}
        </Button>
        <Link
          to="/workspaces/$id/projects/$projectId/tasks"
          params={{ id: workspaceId, projectId }}
        >
          {t('taskWorkflow.backToTasks')}
        </Link>
      </div>
    </section>
  );
}

export default function TaskWorkflowPanel(props: TaskWorkflowPanelProps): ReactElement {
  return (
    <QueryErrorResetBoundary>
      {({ reset }) => (
        <ErrorBoundary
          key={props.taskId}
          onReset={reset}
          fallbackRender={({ error, resetErrorBoundary }) => (
            <TaskWorkflowPanelFallback
              error={error}
              resetErrorBoundary={resetErrorBoundary}
              projectId={props.projectId}
              workspaceId={props.workspaceId}
            />
          )}
        >
          <Suspense fallback={<LoadingCard />}>
            <TaskWorkflowPanelContent key={props.taskId} {...props} />
          </Suspense>
        </ErrorBoundary>
      )}
    </QueryErrorResetBoundary>
  );
}

function TaskWorkflowPanelContent({
  taskId,
  workspaceId,
  projectId,
}: TaskWorkflowPanelProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { t: tConstraints } = useTranslation('constraints');
  const zone = useEffectiveZone();
  const weekStart = useWeekStart();
  const queryClient = useQueryClient();
  const { data: task } = useTaskQuery(taskId);
  const { data: actors } = useTaskActorsQuery(taskId);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const { data: attachments } = useListAttachments(taskId);
  const workflowQuery = useWorkflowQuery(taskId);
  const policyMutation = useSetCompletionPolicy();
  const submissionMutation = useCreateSubmission();
  const decisionMutation = useDecideSubmission();
  const handoffMutation = useCreateHandoff();
  const handoffDecisionMutation = useDecideHandoff();
  const evaluateConstraints = useEvaluateConstraints();
  const [expanded, setExpanded] = useState(false);
  // Set once the user toggles, so the control that replaces the clicked one takes focus.
  const [toggled, setToggled] = useState(false);
  const toggleExpanded = (): void => {
    setToggled(true);
    setExpanded((value) => !value);
  };
  const [criteriaText, setCriteriaText] = useState('');
  const [evidenceText, setEvidenceText] = useState('');
  const [selectedReviewerId, setSelectedReviewerId] = useState('');
  const [selectedAttachmentIds, setSelectedAttachmentIds] = useState<string[]>([]);
  const [selectedConstraintIds, setSelectedConstraintIds] = useState<string[]>([]);
  const [constraintOutcomes, setConstraintOutcomes] = useState<ConstraintOutcome[]>([]);
  const [decisionReason, setDecisionReason] = useState('');
  const [rejecting, setRejecting] = useState(false);
  const [handoffRole, setHandoffRole] = useState<HandoffRole>('assignee');
  const [handoffRecipientId, setHandoffRecipientId] = useState('');
  const [handoffDueOn, setHandoffDueOn] = useState('');
  // `targetId` pins a row-level error (one handoff) to that row.
  const [actionError, setActionError] = useState<{
    scope: ActionScope;
    message: string;
    targetId?: string;
  } | null>(null);
  const failWith = (scope: ActionScope, message: string, targetId?: string): void => {
    setActionError({ scope, message, ...(targetId ? { targetId } : {}) });
  };
  const errorFor = (scope: ActionScope, targetId?: string): ReactElement | null =>
    actionError?.scope === scope && actionError.targetId === targetId ? (
      <p className={targetId ? `${css.error} ${css.rowError}` : css.error} role="alert">
        {actionError.message}
      </p>
    ) : null;

  const snapshot = workflowQuery.data;
  const submission = snapshot?.currentSubmission;
  const currentRevision = snapshot?.workRevision ?? 0;
  const submissionIsStale = Boolean(submission && submission.revision !== currentRevision);
  const currentDecision = submissionIsStale ? undefined : submission?.decision;
  const reviewerActors = getRoleActors(actors);
  const memberName = (userId: string | undefined): string | undefined =>
    members.find((member) => member.userId === userId)?.displayName;
  const reviewerName = (userId: string | undefined): string =>
    memberName(userId) ??
    (userId ? t('taskWorkflow.unknownReviewer') : t('taskWorkflow.noReviewer'));
  const attachmentById = new Map(attachments.map((attachment) => [attachment.id, attachment]));
  const attachmentVersion = attachments
    .map(
      (attachment) => `${attachment.id}:${attachment.checksumSha256 ?? ''}:${attachment.createdAt}`,
    )
    .join('|');

  const contentFingerprint = `${task.title}\u0000${task.description ?? ''}\u0000${attachmentVersion}`;
  const previousFingerprint = useRef(contentFingerprint);
  useEffect(() => {
    if (previousFingerprint.current === contentFingerprint) return;
    previousFingerprint.current = contentFingerprint;
    void queryClient.invalidateQueries({ queryKey: workflowKeys.detail(taskId) });
  }, [contentFingerprint, queryClient, taskId]);

  useEffect(() => {
    if (!snapshot) return;
    const current = snapshot.currentSubmission?.reviewerUserId;
    const roleActors = getRoleActors(actors);
    const nextReviewer =
      roleActors.find((actor) => actor.userId === current)?.userId ?? roleActors[0]?.userId ?? '';
    setSelectedReviewerId((previous) => (previous === nextReviewer ? previous : nextReviewer));
    setSelectedConstraintIds((previous) => {
      const next = snapshot.requiredConstraintIds ?? [];
      return previous.length === next.length && previous.every((id, index) => id === next[index])
        ? previous
        : [...next];
    });
  }, [actors, snapshot]);

  useEffect(() => {
    if (
      snapshot?.completionPolicy === 'review_required' ||
      snapshot?.currentSubmission ||
      snapshot?.handoffs?.some((handoff) => handoff.status === 'pending')
    ) {
      setExpanded(true);
    }
  }, [snapshot]);

  const allowedHandoffRoles = (snapshot?.capabilities.canRequestHandoffRoles ?? []).filter(
    isHandoffRole,
  );
  useEffect(() => {
    if (allowedHandoffRoles.length > 0 && !allowedHandoffRoles.includes(handoffRole)) {
      setHandoffRole(allowedHandoffRoles[0] ?? 'assignee');
    }
  }, [allowedHandoffRoles, handoffRole]);

  if (workflowQuery.isError) {
    return (
      <ErrorCard
        projectId={projectId}
        workspaceId={workspaceId}
        onRetry={() => {
          void workflowQuery.refetch();
        }}
      />
    );
  }
  if (workflowQuery.isPending || !snapshot) {
    return (
      <div data-compact="" role="status" aria-label={t('taskWorkflow.loading')}>
        <Skeleton
          style={{ blockSize: 'var(--nf-space-8)', inlineSize: 'calc(var(--nf-space-8) * 4)' }}
        />
      </div>
    );
  }

  const enabledEvidence = splitLines(evidenceText);
  const evidenceValid = enabledEvidence.every((value) => safeSourceUrl(value) !== undefined);
  const criteria = splitLines(criteriaText);
  const canCreateSubmission =
    snapshot.capabilities.canSubmit &&
    (!submission || submissionIsStale || currentDecision?.decision === 'rejected');
  const canSubmitCurrent =
    canCreateSubmission && Boolean(selectedReviewerId) && criteria.length > 0 && evidenceValid;
  const showDecisionActions =
    Boolean(submission) &&
    !submissionIsStale &&
    !currentDecision &&
    snapshot.capabilities.canDecideCurrentSubmission;
  const hasActiveReview =
    snapshot.completionPolicy === 'review_required' || Boolean(submission) || submissionIsStale;
  const hasPendingHandoff = (snapshot.handoffs ?? []).some(
    (handoff) => handoff.status === 'pending',
  );

  // Manual completion with nothing in flight: one button in the page's "Add to this task" row.
  if (!expanded && !hasActiveReview && !hasPendingHandoff) {
    return (
      <div data-compact="">
        <TaskActionButton
          icon={<UserCheck size={14} aria-hidden />}
          aria-expanded={false}
          autoFocus={toggled}
          onClick={toggleExpanded}
        >
          {t('taskWorkflow.title')}
        </TaskActionButton>
      </div>
    );
  }

  const handleSetPolicy = async (policy: 'manual' | 'review_required'): Promise<void> => {
    setActionError(null);
    try {
      await policyMutation.mutateAsync({
        taskId,
        input: { policy, requiredConstraintIds: policy === 'manual' ? [] : selectedConstraintIds },
      });
    } catch (error) {
      failWith('policy', formatApiError(error, t, 'taskWorkflow.saveFailed'));
    }
  };

  const handleSaveRequiredConstraints = async (): Promise<void> => {
    setActionError(null);
    try {
      await policyMutation.mutateAsync({
        taskId,
        input: { policy: 'review_required', requiredConstraintIds: selectedConstraintIds },
      });
    } catch (error) {
      failWith('checks', formatApiError(error, t, 'taskWorkflow.saveFailed'));
    }
  };

  const handleLoadConstraints = async (): Promise<void> => {
    setActionError(null);
    try {
      const result = await evaluateConstraints.mutateAsync({ taskId });
      setConstraintOutcomes(result.outcomes);
    } catch (error) {
      failWith('checks', formatApiError(error, t, 'taskWorkflow.constraintsLoadFailed'));
    }
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (!canSubmitCurrent) return;
    setActionError(null);
    try {
      await submissionMutation.mutateAsync({
        taskId,
        input: {
          reviewerUserId: selectedReviewerId,
          acceptanceCriteria: criteria,
          evidenceUrls: enabledEvidence.map((value) => safeSourceUrl(value) as string),
          attachmentIds: selectedAttachmentIds,
        },
      });
    } catch (error) {
      failWith('submit', formatApiError(error, t, 'taskWorkflow.submitFailed'));
    }
  };

  const handleDecision = async (decision: 'approve' | 'reject'): Promise<void> => {
    if (!submission || submissionIsStale) return;
    if (decision === 'reject' && !decisionReason.trim()) {
      setRejecting(true);
      failWith('reason', t('taskWorkflow.reasonRequired'));
      return;
    }
    setActionError(null);
    try {
      await decisionMutation.mutateAsync({
        taskId,
        submissionId: submission.id,
        input: {
          decision,
          ...(decision === 'reject' ? { reason: decisionReason.trim() } : {}),
        },
      });
      setRejecting(false);
      setDecisionReason('');
    } catch (error) {
      failWith('decision', formatApiError(error, t, 'taskWorkflow.decisionFailed'));
    }
  };

  const handleCreateHandoff = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (!handoffRecipientId || !allowedHandoffRoles.includes(handoffRole)) return;
    setActionError(null);
    try {
      await handoffMutation.mutateAsync({
        taskId,
        input: {
          role: handoffRole,
          toUserId: handoffRecipientId,
          ...(handoffDueOn ? { dueAt: endOfDayUnix(handoffDueOn, zone) } : {}),
        },
      });
      setHandoffRecipientId('');
      setHandoffDueOn('');
    } catch (error) {
      failWith('handoff', formatApiError(error, t, 'taskWorkflow.handoffFailed'));
    }
  };

  const handleHandoffDecision = async (
    handoff: WorkflowHandoff,
    decision: 'accept' | 'decline' | 'cancel',
  ): Promise<void> => {
    if (handoffDecisionMutation.isPending) return;
    if (decision === 'cancel') {
      const confirmed = await confirmAction({
        message: t('taskWorkflow.withdrawHandoffConfirm', {
          recipient: memberName(handoff.toUserId) ?? t('taskWorkflow.unknownMember'),
        }),
        confirmLabel: t('taskWorkflow.withdrawHandoff'),
        tone: 'danger',
      });
      if (!confirmed) return;
    }
    setActionError(null);
    try {
      await handoffDecisionMutation.mutateAsync({
        taskId,
        handoffId: handoff.id,
        input: { decision },
      });
    } catch (error) {
      failWith(
        'handoffDecision',
        formatApiError(
          error,
          t,
          decision === 'cancel'
            ? 'taskWorkflow.handoffWithdrawFailed'
            : 'taskWorkflow.handoffDecisionFailed',
        ),
        handoff.id,
      );
    }
  };

  const locale = i18n.resolvedLanguage ?? i18n.language;
  const weekdayLabels: unknown = t('common.date.weekdays', { returnObjects: true });
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const handoffRoleLabel = (role: string): string =>
    isHandoffRole(role) ? t(HANDOFF_ROLE_KEY[role]) : role;
  const displayedPolicy = policyMutation.isPending
    ? policyMutation.variables.input.policy
    : snapshot.completionPolicy;
  const pendingDecision = decisionMutation.isPending
    ? decisionMutation.variables.input.decision
    : undefined;
  const pendingHandoffDecision = handoffDecisionMutation.isPending
    ? handoffDecisionMutation.variables
    : undefined;
  const currentSubmissionAttachments = submission?.attachments ?? [];
  const recentSubmissions = snapshot.recentSubmissions ?? [];
  const isPolicyEditable = snapshot.capabilities.canSetPolicy;

  return (
    <section className={css.card}>
      <TaskSectionHeading
        title={t('taskWorkflow.title')}
        hint={t('taskWorkflow.description')}
        trailing={
          <TaskActionButton
            icon={
              expanded ? <ChevronUp size={14} aria-hidden /> : <ChevronDown size={14} aria-hidden />
            }
            aria-expanded={expanded}
            autoFocus={toggled}
            onClick={toggleExpanded}
          >
            {expanded ? t('taskWorkflow.collapse') : t('taskWorkflow.expand')}
          </TaskActionButton>
        }
      />
      <div className={css.summary}>
        <span className={css.numeric}>
          {t('taskWorkflow.revision', { revision: currentRevision })}
        </span>
        <Badge tone={hasActiveReview ? 'info' : 'neutral'}>
          {t(
            snapshot.completionPolicy === 'review_required'
              ? 'taskWorkflow.reviewRequired'
              : 'taskWorkflow.manual',
          )}
        </Badge>
        {snapshot.requiredConstraintIds?.length ? (
          <span className={css.numeric}>
            {t('taskWorkflow.requiredChecksCount', {
              count: snapshot.requiredConstraintIds.length,
            })}
          </span>
        ) : null}
      </div>

      {!expanded ? null : (
        <>
          <p className={css.muted}>{t('taskWorkflow.revision_hint')}</p>
          {isPolicyEditable ? (
            <div className={css.section}>
              <h3 className={css.sectionTitle}>{t('taskWorkflow.completionPolicy')}</h3>
              <SegmentedControl<'manual' | 'review_required'>
                ariaLabel={t('taskWorkflow.completionPolicy')}
                size="sm"
                value={displayedPolicy === 'review_required' ? 'review_required' : 'manual'}
                disabled={policyMutation.isPending}
                options={[
                  { value: 'manual', label: t('taskWorkflow.manual') },
                  { value: 'review_required', label: t('taskWorkflow.reviewRequired') },
                ]}
                onChange={(policy) => {
                  if (policy !== snapshot.completionPolicy) void handleSetPolicy(policy);
                }}
              />
              <p className={css.muted}>{t('taskWorkflow.policyHelp')}</p>
              {errorFor('policy')}
            </div>
          ) : null}

          {snapshot.completionPolicy === 'review_required' ? (
            <section className={css.section} aria-label={t('taskWorkflow.requiredChecks')}>
              <h3 className={css.sectionTitle}>{t('taskWorkflow.requiredChecks')}</h3>
              {(snapshot.requiredConstraintIds ?? []).length === 0 ? (
                <p className={css.muted}>{t('taskWorkflow.noRequiredChecks')}</p>
              ) : (
                <ul className={css.list}>
                  {(snapshot.requiredConstraintIds ?? []).map((id) => (
                    <li key={id} className={css.checkId}>
                      {id}
                    </li>
                  ))}
                </ul>
              )}
              {isPolicyEditable ? (
                <>
                  <div className={css.row}>
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      disabled={evaluateConstraints.isPending}
                      aria-busy={evaluateConstraints.isPending || undefined}
                      onClick={() => {
                        void handleLoadConstraints();
                      }}
                    >
                      {evaluateConstraints.isPending
                        ? t('taskWorkflow.loadingChecks')
                        : t('taskWorkflow.loadChecks')}
                    </Button>
                  </div>
                  {constraintOutcomes.length > 0 ? (
                    <ul className={css.list}>
                      {constraintOutcomes.map((outcome) => (
                        <li key={outcome.id} className={css.checkRow}>
                          <Checkbox
                            aria-label={t('taskWorkflow.requiredCheckNamed', {
                              kind: tConstraints(KIND_LABEL_KEY[outcome.kind]),
                              expression: outcome.expression,
                            })}
                            checked={selectedConstraintIds.includes(outcome.id)}
                            disabled={Boolean(outcome.parseError)}
                            onChange={(event) => {
                              setSelectedConstraintIds((current) =>
                                event.currentTarget.checked
                                  ? [...new Set([...current, outcome.id])]
                                  : current.filter((id) => id !== outcome.id),
                              );
                            }}
                          />
                          <span className={css.checkKind}>
                            {tConstraints(KIND_LABEL_KEY[outcome.kind])}
                          </span>
                          <code className={css.checkId} title={outcome.expression}>
                            {outcome.expression}
                          </code>
                          {outcome.parseError ? (
                            <span className={css.muted}>{t('taskWorkflow.unsupportedCheck')}</span>
                          ) : (
                            <Badge tone={outcome.satisfied ? 'success' : 'warning'}>
                              {t(
                                outcome.satisfied
                                  ? 'taskWorkflow.checkSatisfied'
                                  : 'taskWorkflow.checkOpen',
                              )}
                            </Badge>
                          )}
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  {errorFor('checks')}
                  <div className={css.actions}>
                    <Button
                      type="button"
                      size="sm"
                      disabled={policyMutation.isPending}
                      aria-busy={policyMutation.isPending || undefined}
                      onClick={() => {
                        void handleSaveRequiredConstraints();
                      }}
                    >
                      {policyMutation.isPending
                        ? t('taskWorkflow.saving')
                        : t('taskWorkflow.saveRequiredChecks')}
                    </Button>
                  </div>
                </>
              ) : null}
            </section>
          ) : null}

          {submission ? (
            <section className={css.section} aria-label={t('taskWorkflow.currentSubmission')}>
              <h3 className={css.sectionTitle}>{t('taskWorkflow.currentSubmission')}</h3>
              <p className={css.summary}>
                {t('taskWorkflow.reviewerNamed', { name: reviewerName(submission.reviewerUserId) })}
              </p>
              {submissionIsStale ? (
                <p className={css.notice} role="status">
                  {t(
                    submission.decision?.decision === 'approved'
                      ? 'taskWorkflow.staleApproval'
                      : 'taskWorkflow.staleSubmission',
                    {
                      submittedRevision: submission.revision,
                      currentRevision,
                    },
                  )}
                </p>
              ) : null}
              {submissionIsStale ? (
                submission.decision?.decision === 'rejected' && submission.decision.reason ? (
                  <p className={css.muted}>
                    <strong className={css.label}>
                      {t('taskWorkflow.previousReviewFeedback')}
                    </strong>
                    {submission.decision.reason}
                  </p>
                ) : null
              ) : currentDecision?.decision === 'approved' ? (
                <p className={css.summary}>
                  <Badge tone="success">{t('taskWorkflow.approved')}</Badge>
                </p>
              ) : currentDecision?.decision === 'rejected' ? (
                <div className={css.reason}>
                  <p className={css.summary}>
                    <Badge tone="warning">{t('taskWorkflow.rejected')}</Badge>
                  </p>
                  {currentDecision.reason ? (
                    <p className={css.muted}>
                      <strong className={css.label}>
                        {t('taskWorkflow.previousReviewFeedback')}
                      </strong>
                      {currentDecision.reason}
                    </p>
                  ) : null}
                </div>
              ) : (
                <p className={css.summary}>
                  <Badge tone="warning">{t('taskWorkflow.pendingReview')}</Badge>
                </p>
              )}

              <div className={css.itemBody}>
                <strong>{t('taskWorkflow.acceptanceCriteria')}</strong>
                {(submission.acceptanceCriteria ?? []).length ? (
                  <ul className={css.list}>
                    {(submission.acceptanceCriteria ?? []).map((criterion) => (
                      <li key={criterion}>{criterion}</li>
                    ))}
                  </ul>
                ) : (
                  <p className={css.muted}>{t('taskWorkflow.noCriteria')}</p>
                )}
              </div>
              <div className={css.itemBody}>
                <strong>{t('taskWorkflow.evidence')}</strong>
                {(submission.evidenceUrls ?? []).length ? (
                  <ul className={css.list}>
                    {(submission.evidenceUrls ?? []).map((value) => {
                      const url = safeSourceUrl(value);
                      return (
                        <li key={value}>
                          {url ? (
                            <a className={css.safeLink} href={url} target="_blank" rel="noreferrer">
                              {url}
                            </a>
                          ) : (
                            <span>{value}</span>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className={css.muted}>{t('taskWorkflow.noEvidence')}</p>
                )}
              </div>
              {currentSubmissionAttachments.length > 0 ? (
                <div className={css.itemBody}>
                  <strong>{t('taskWorkflow.attachments')}</strong>
                  <ul className={css.list}>
                    {currentSubmissionAttachments.map((attachment) => (
                      <li key={attachment.id}>
                        {attachmentById.get(attachment.id)?.filename ?? attachment.sha256}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {showDecisionActions ? (
                <form
                  className={css.form}
                  onSubmit={(event) => {
                    event.preventDefault();
                    if (rejecting) void handleDecision('reject');
                  }}
                  onKeyDown={onSubmitShortcut}
                >
                  {rejecting ? (
                    <FormField
                      label={t('taskWorkflow.rejectionReason')}
                      required
                      {...(actionError?.scope === 'reason' ? { error: actionError.message } : {})}
                    >
                      {(control) => (
                        <Textarea
                          {...control}
                          value={decisionReason}
                          rows={3}
                          onChange={(event) => {
                            setDecisionReason(event.currentTarget.value);
                            if (actionError?.scope === 'reason') setActionError(null);
                          }}
                        />
                      )}
                    </FormField>
                  ) : null}
                  {errorFor('decision')}
                  {rejecting ? (
                    <FormActions
                      hint={{ disabled: decisionMutation.isPending || !decisionReason.trim() }}
                    >
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={decisionMutation.isPending}
                        onClick={() => setRejecting(false)}
                      >
                        {t('taskWorkflow.cancel')}
                      </Button>
                      <Button
                        type="submit"
                        variant="danger"
                        disabled={decisionMutation.isPending || !decisionReason.trim()}
                        aria-busy={pendingDecision === 'reject' || undefined}
                      >
                        {pendingDecision === 'reject'
                          ? t('taskWorkflow.saving')
                          : t('taskWorkflow.rejectSubmission')}
                      </Button>
                    </FormActions>
                  ) : (
                    <div className={css.actions}>
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={decisionMutation.isPending}
                        onClick={() => setRejecting(true)}
                      >
                        {t('taskWorkflow.reject')}
                      </Button>
                      <Button
                        type="button"
                        variant="primary"
                        disabled={decisionMutation.isPending}
                        aria-busy={pendingDecision === 'approve' || undefined}
                        onClick={() => {
                          void handleDecision('approve');
                        }}
                      >
                        {pendingDecision === 'approve'
                          ? t('taskWorkflow.saving')
                          : t('taskWorkflow.approve')}
                      </Button>
                    </div>
                  )}
                </form>
              ) : null}
            </section>
          ) : null}

          {recentSubmissions.length > 0 ? (
            <section className={css.section} aria-label={t('taskWorkflow.submissionHistory')}>
              <details className={css.history}>
                <summary className={css.historySummary}>
                  <span>{t('taskWorkflow.submissionHistory')}</span>
                  <Badge tone="neutral" className={css.numeric}>
                    {recentSubmissions.length}
                  </Badge>
                </summary>
                <ol className={css.list}>
                  {recentSubmissions.map((historySubmission) => {
                    const decision = historySubmission.decision;
                    const isStaleApproval =
                      decision?.decision === 'approved' &&
                      historySubmission.revision !== currentRevision;
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
          ) : null}

          {canCreateSubmission ? (
            <section className={css.section} aria-label={t('taskWorkflow.submitWork')}>
              <h3 className={css.sectionTitle}>
                {submission ? t('taskWorkflow.resubmitWork') : t('taskWorkflow.submitWork')}
              </h3>
              {reviewerActors.length === 0 ? (
                <p className={css.muted}>{t('taskWorkflow.assignReviewerFirst')}</p>
              ) : (
                <form
                  className={css.form}
                  onSubmit={(event) => void handleSubmit(event)}
                  onKeyDown={onSubmitShortcut}
                >
                  <p className={css.muted}>{t('taskWorkflow.submitStartsReview')}</p>
                  <FormField label={t('taskWorkflow.reviewer')} required>
                    {() => (
                      <Combobox
                        aria-label={t('taskWorkflow.reviewer')}
                        placeholder={t('taskWorkflow.reviewer')}
                        emptyMessage={t('taskWorkflow.noReviewers')}
                        value={selectedReviewerId}
                        options={reviewerActors.map((actor) => ({
                          value: actor.userId,
                          label: actor.displayName,
                        }))}
                        onChange={setSelectedReviewerId}
                      />
                    )}
                  </FormField>
                  <FormField label={t('taskWorkflow.acceptanceCriteria')} required>
                    {(control) => (
                      <Textarea
                        {...control}
                        value={criteriaText}
                        rows={3}
                        placeholder={t('taskWorkflow.oneCriterionPerLine')}
                        onChange={(event) => setCriteriaText(event.currentTarget.value)}
                      />
                    )}
                  </FormField>
                  <FormField
                    label={t('taskWorkflow.evidenceUrls')}
                    description={t('taskWorkflow.evidenceHelp')}
                    {...(evidenceValid ? {} : { error: t('taskWorkflow.invalidEvidenceUrl') })}
                  >
                    {(control) => (
                      <Textarea
                        {...control}
                        value={evidenceText}
                        rows={2}
                        placeholder={t('taskWorkflow.evidenceUrlPlaceholder')}
                        dir="ltr"
                        onChange={(event) => setEvidenceText(event.currentTarget.value)}
                      />
                    )}
                  </FormField>
                  {attachments.length > 0 ? (
                    <fieldset className={css.fieldset}>
                      <legend className={css.sectionTitle}>
                        {t('taskWorkflow.includeAttachments')}
                      </legend>
                      <ul className={css.list}>
                        {attachments.map((attachment) => (
                          <li key={attachment.id} className={css.checkRow}>
                            <Checkbox
                              aria-label={t('taskWorkflow.includeAttachmentNamed', {
                                name: attachment.filename,
                              })}
                              checked={selectedAttachmentIds.includes(attachment.id)}
                              onChange={(event) =>
                                setSelectedAttachmentIds((current) =>
                                  event.currentTarget.checked
                                    ? [...new Set([...current, attachment.id])]
                                    : current.filter((id) => id !== attachment.id),
                                )
                              }
                            />
                            <span>{attachment.filename}</span>
                          </li>
                        ))}
                      </ul>
                    </fieldset>
                  ) : null}
                  {errorFor('submit')}
                  <FormActions
                    hint={{ disabled: !canSubmitCurrent || submissionMutation.isPending }}
                  >
                    <Button
                      type="submit"
                      variant="primary"
                      disabled={!canSubmitCurrent || submissionMutation.isPending}
                      aria-busy={submissionMutation.isPending || undefined}
                    >
                      {submissionMutation.isPending
                        ? t('taskWorkflow.submitting')
                        : t(submission ? 'taskWorkflow.resubmitWork' : 'taskWorkflow.submitWork')}
                    </Button>
                  </FormActions>
                </form>
              )}
            </section>
          ) : null}

          {allowedHandoffRoles.length > 0 ? (
            <section className={css.section} aria-label={t('taskWorkflow.handoffs')}>
              <h3 className={css.sectionTitle}>{t('taskWorkflow.handoffs')}</h3>
              <p className={css.muted}>{t('taskWorkflow.handoffs_hint')}</p>
              <form className={css.form} onSubmit={(event) => void handleCreateHandoff(event)}>
                <SegmentedControl<HandoffRole>
                  ariaLabel={t('taskWorkflow.handoffRole')}
                  value={
                    allowedHandoffRoles.includes(handoffRole)
                      ? handoffRole
                      : (allowedHandoffRoles[0] ?? null)
                  }
                  fullWidth
                  disabled={handoffMutation.isPending}
                  options={allowedHandoffRoles.map((role) => ({
                    value: role,
                    label: t(HANDOFF_ROLE_KEY[role]),
                  }))}
                  onChange={setHandoffRole}
                />
                <FormField label={t('taskWorkflow.recipient')} required>
                  {() => (
                    <Combobox
                      aria-label={t('taskWorkflow.recipient')}
                      placeholder={t('taskWorkflow.recipient')}
                      emptyMessage={t('taskWorkflow.noAvailableMembers')}
                      value={handoffRecipientId}
                      options={members.map((member) => ({
                        value: member.userId,
                        label: member.displayName,
                      }))}
                      onChange={setHandoffRecipientId}
                    />
                  )}
                </FormField>
                <FormField label={t('taskWorkflow.handoffDueDate')}>
                  {() => (
                    <DatePicker
                      value={handoffDueOn}
                      onChange={setHandoffDueOn}
                      {...(Array.isArray(weekdayLabels) ? { weekdayLabels } : {})}
                      weekStart={weekStart}
                      formatMonthYear={formatMonthYear}
                      prevLabel={t('calendar.prev')}
                      nextLabel={t('calendar.next')}
                      triggerLabel={
                        handoffDueOn
                          ? formatDateOnly(handoffDueOn, locale)
                          : t('common.date.placeholder')
                      }
                      onClear={() => setHandoffDueOn('')}
                      clearLabel={t('common.date.clear')}
                    />
                  )}
                </FormField>
                {errorFor('handoff')}
                <div className={css.actions}>
                  <Button
                    type="submit"
                    disabled={!handoffRecipientId || handoffMutation.isPending}
                    aria-busy={handoffMutation.isPending || undefined}
                  >
                    {handoffMutation.isPending
                      ? t('taskWorkflow.requestingHandoff')
                      : t('taskWorkflow.requestHandoff')}
                  </Button>
                </div>
              </form>
            </section>
          ) : null}

          {(snapshot.handoffs ?? []).length > 0 ? (
            <section className={css.section} aria-label={t('taskWorkflow.handoffHistory')}>
              <h3 className={css.sectionTitle}>{t('taskWorkflow.handoffHistory')}</h3>
              <ul className={`${css.list} ${css.dividedList}`}>
                {(snapshot.handoffs ?? []).map((handoff) => {
                  const status = isHandoffStatus(handoff.status) ? handoff.status : undefined;
                  const deciding =
                    pendingHandoffDecision?.handoffId === handoff.id
                      ? pendingHandoffDecision.input.decision
                      : undefined;
                  const canWithdraw = status === 'pending' && handoff.canCancel;
                  return (
                    <li key={handoff.id} className={css.handoffItem}>
                      <div className={css.itemBody}>
                        <div className={css.itemHeader}>
                          <strong>
                            {t('taskWorkflow.handoffSummary', {
                              role: handoffRoleLabel(handoff.role),
                              recipient:
                                memberName(handoff.toUserId) ?? t('taskWorkflow.unknownMember'),
                            })}
                          </strong>
                          <Badge tone={status ? HANDOFF_STATUS_TONE[status] : 'neutral'}>
                            {status ? t(HANDOFF_STATUS_KEY[status]) : handoff.status}
                          </Badge>
                          {handoff.dueAt ? (
                            <span className={`${css.muted} ${css.numeric}`}>
                              {t('taskWorkflow.handoffDueSuffix', {
                                due: formatEpoch(handoff.dueAt, locale, zone) ?? '',
                              })}
                            </span>
                          ) : null}
                        </div>
                        {status === 'pending' ? (
                          <p className={css.muted}>{t('taskWorkflow.handoffPendingHint')}</p>
                        ) : status === 'expired' ? (
                          <p className={css.muted}>{t('taskWorkflow.handoffExpiredHint')}</p>
                        ) : null}
                      </div>
                      {status === 'pending' && (handoff.canDecide || canWithdraw) ? (
                        <div className={css.row}>
                          {canWithdraw ? (
                            <Button
                              type="button"
                              size="sm"
                              variant="danger"
                              disabled={handoffDecisionMutation.isPending}
                              aria-busy={deciding === 'cancel' || undefined}
                              onClick={() => void handleHandoffDecision(handoff, 'cancel')}
                            >
                              {deciding === 'cancel'
                                ? t('taskWorkflow.withdrawingHandoff')
                                : t('taskWorkflow.withdrawHandoff')}
                            </Button>
                          ) : null}
                          {handoff.canDecide ? (
                            <>
                              <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                disabled={handoffDecisionMutation.isPending}
                                aria-busy={deciding === 'decline' || undefined}
                                onClick={() => void handleHandoffDecision(handoff, 'decline')}
                              >
                                {deciding === 'decline'
                                  ? t('taskWorkflow.saving')
                                  : t('taskWorkflow.declineHandoff')}
                              </Button>
                              <Button
                                type="button"
                                size="sm"
                                variant="primary"
                                disabled={handoffDecisionMutation.isPending}
                                aria-busy={deciding === 'accept' || undefined}
                                onClick={() => void handleHandoffDecision(handoff, 'accept')}
                              >
                                {deciding === 'accept'
                                  ? t('taskWorkflow.saving')
                                  : t('taskWorkflow.acceptHandoff')}
                              </Button>
                            </>
                          ) : null}
                        </div>
                      ) : null}
                      {errorFor('handoffDecision', handoff.id)}
                    </li>
                  );
                })}
              </ul>
            </section>
          ) : null}
        </>
      )}
    </section>
  );
}

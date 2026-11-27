/**
 * TaskCreateDialog — modal form to create a new task in a project.
 *
 * Includes an optional AI Assist section that calls the smart-create API
 * to suggest subtask decomposition and assignees based on past ticket
 * patterns. The AI Assist feature requires a workspaceId prop and is
 * hidden when no workspace context is available.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, Suspense, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import FormActions from '../../components/form-actions';
import { formatApiError } from '../../lib/api-error';
import { formatDate } from '../../lib/format';
import { onSubmitShortcut } from '../../lib/submit-shortcut';
import { useSubmitGuard } from '../../lib/use-submit-guard';
import { useWeekStart } from '../../lib/use-week-start';
import { AiSparkle } from '../ai-suggestions/ai-consequence';
import { selectUser, useAuth } from '../auth/auth-store';
import { useWorkspaceMembersQuery } from '../workspaces/api';
import { type TaskPriority, useCreateTask } from './api';
import PriorityControl from './priority-control';
import {
  type AssigneeSuggestion,
  type SmartProposal,
  type SubtaskProposal,
  useApplySmartTask,
  useProposeSmartTask,
} from './smart-create-api';
import styles from './task-create-dialog.module.css';
import { useTaskFormState } from './use-task-form-state';

export interface TaskCreateDialogProps {
  projectId: string;
  /** Workspace ID required for AI Assist. When omitted, the button is hidden. */
  workspaceId?: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  title?: string;
  dueOn?: string;
}

const schema = z.object({
  title: z.string().min(1, 'tasks.validation.title_required').max(500),
  description: z.string().max(50000).optional(),
  priority: z.number().int().min(0).max(4),
  dueOn: z
    .string()
    .regex(/^$|^\d{4}-\d{2}-\d{2}$/, 'tasks.validation.due_format')
    .optional(),
});

/** AI subtask priorities arrive as words; tones follow `PRIORITY_TONE` in constants. */
const PRIORITY_TONE: Record<string, BadgeTone> = {
  low: 'neutral',
  medium: 'info',
  high: 'warning',
  urgent: 'danger',
};

function priorityTone(label: string): BadgeTone {
  return PRIORITY_TONE[label] ?? 'neutral';
}

export default function TaskCreateDialog({
  projectId,
  workspaceId,
  open,
  onClose,
}: TaskCreateDialogProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const weekStart = useWeekStart();
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const create = useCreateTask();
  const propose = useProposeSmartTask();
  const applySmartMutation = useApplySmartTask();

  const { title, description, setTitle, setDescription, reset: resetCore } = useTaskFormState();
  const [priority, setPriority] = useState<TaskPriority>(2);
  const [dueOn, setDueOn] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const { guard, submitting, end } = useSubmitGuard();
  const priorityLabelId = useId();

  // The assignee defaults to the caller, who is already known synchronously,
  // so the member list loading never holds up typing or submitting.
  const self = useAuth(selectUser);
  const selfId = self?.id;
  const [pickedAssignee, setPickedAssignee] = useState<string | null>(null);
  const assigneeId = pickedAssignee ?? selfId;

  // AI Assist state
  const [proposal, setProposal] = useState<SmartProposal | null>(null);
  const [selectedAssignees, setSelectedAssignees] = useState<Set<string>>(new Set());
  const [selectedSubtasks, setSelectedSubtasks] = useState<Set<number>>(new Set());
  const [proposalError, setProposalError] = useState<string | null>(null);

  const reset = (): void => {
    resetCore();
    setPriority(2);
    setDueOn('');
    setPickedAssignee(null);
    setErrors({});
    setProposal(null);
    setSelectedAssignees(new Set());
    setSelectedSubtasks(new Set());
    setProposalError(null);
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handlePropose = (): void => {
    if (!workspaceId || title.trim().length === 0) return;
    setProposalError(null);
    propose.mutate(
      {
        workspaceId,
        projectId,
        title: title.trim(),
        description: description.trim(),
      },
      {
        onSuccess: (data) => {
          setProposal(data);
          // Select all assignees and subtasks by default
          setSelectedAssignees(new Set(data.suggestedAssignees.map((a) => a.userPublicId)));
          setSelectedSubtasks(new Set(data.subtasks.map((_, i) => i)));
        },
        onError: (error) => {
          setProposalError(error.code ?? '');
        },
      },
    );
  };

  const handleToggleAssignee = (id: string): void => {
    setSelectedAssignees((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const handleToggleSubtask = (idx: number): void => {
    setSelectedSubtasks((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) {
        next.delete(idx);
      } else {
        next.add(idx);
      }
      return next;
    });
  };

  const handleApplySmart = async (): Promise<void> => {
    if (!workspaceId || !proposal) return;
    if (guard()) return;
    try {
      const subtasks = proposal.subtasks
        .filter((_, i) => selectedSubtasks.has(i))
        .map((st) => {
          const base: { title: string; description: string; priority: number } = {
            title: st.title,
            description: st.description,
            priority: priorityNumber(st.priority),
          };
          if (st.assignee && selectedAssignees.has(st.assignee.userPublicId)) {
            return { ...base, assigneeUserId: st.assignee.userPublicId };
          }
          return base;
        });

      await applySmartMutation.mutateAsync({
        workspaceId,
        projectId,
        title: title.trim(),
        description: description.trim(),
        priority,
        ...(dueOn ? { dueOn } : {}),
        // apply-smart never self-assigns, so the field value always goes in.
        assigneeUserIds: [...new Set([...selectedAssignees, ...(assigneeId ? [assigneeId] : [])])],
        subtasks,
      });
      reset();
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.create_failed'),
      });
    } finally {
      end();
    }
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedDescription = description.trim();
    const parsed = schema.safeParse({
      title: trimmedTitle,
      description: trimmedDescription === '' ? undefined : trimmedDescription,
      priority,
      dueOn: dueOn === '' ? undefined : dueOn,
    });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'title') next.title = issue.message;
        if (field === 'dueOn') next.dueOn = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    if (guard()) return;
    // Omitting `actors` makes the server assign the caller, so only another
    // member needs to be spelled out.
    const otherAssignee = workspaceId && assigneeId && assigneeId !== selfId ? assigneeId : null;
    try {
      await create.mutateAsync({
        projectId,
        title: parsed.data.title,
        ...(parsed.data.description ? { description: parsed.data.description } : {}),
        priority: parsed.data.priority as TaskPriority,
        ...(parsed.data.dueOn ? { dueOn: parsed.data.dueOn } : {}),
        visibility: 'public' as const,
        ...(otherAssignee
          ? { actors: [{ role: 'assignee' as const, userId: otherAssignee }] }
          : {}),
      });
      reset();
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.create_failed'),
      });
    } finally {
      end();
    }
  };

  const hasProposal =
    proposal !== null && (proposal.suggestedAssignees.length > 0 || proposal.subtasks.length > 0);

  return (
    <Dialog open={open} onClose={handleClose} title={t('tasks.new')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        className={styles.form}
        onKeyDown={onSubmitShortcut}
      >
        <div className={styles.fieldWithError}>
          <FormField
            label={t('tasks.form.title')}
            required
            {...(errors.title ? { error: t(errors.title) } : {})}
          >
            {(control) => (
              <Input
                {...control}
                value={title}
                onChange={(e) => {
                  setTitle(e.target.value);
                  if (errors.title) {
                    setErrors((prev) => {
                      const { title: T, ...rest } = prev;
                      return rest;
                    });
                  }
                }}
                autoFocus
              />
            )}
          </FormField>
        </div>

        <FormField label={t('tasks.form.description')}>
          {(control) => (
            <Textarea
              {...control}
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
              rows={4}
            />
          )}
        </FormField>

        <div className={styles.priorityField}>
          <span id={priorityLabelId} className={styles.priorityLabel}>
            {t('tasks.form.priority')}
          </span>
          <div role="group" aria-labelledby={priorityLabelId}>
            <PriorityControl value={priority} onChange={setPriority} />
          </div>
        </div>

        {workspaceId && assigneeId ? (
          <FormField label={t('tasks.form.assignee')} description={t('tasks.form.assignee_hint')}>
            {(control) => (
              <Suspense fallback={<Skeleton style={{ blockSize: 'var(--nf-control-height)' }} />}>
                <AssigneeCombobox
                  id={control.id}
                  workspaceId={workspaceId}
                  selfId={selfId}
                  selfName={self?.displayName ?? ''}
                  value={assigneeId}
                  onChange={setPickedAssignee}
                />
              </Suspense>
            )}
          </FormField>
        ) : null}

        <div className={styles.fieldWithError}>
          <FormField
            label={t('tasks.form.due')}
            {...(errors.dueOn ? { error: t(errors.dueOn) } : {})}
          >
            {() => (
              <DatePicker
                variant="field"
                value={dueOn}
                onChange={(value) => {
                  setDueOn(value);
                  if (errors.dueOn) {
                    setErrors((prev) => {
                      const { dueOn: D, ...rest } = prev;
                      return rest;
                    });
                  }
                }}
                weekdayLabels={weekdayLabels}
                weekStart={weekStart}
                formatMonthYear={formatMonthYear}
                prevLabel={t('calendar.prev')}
                nextLabel={t('calendar.next')}
                triggerLabel={dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder')}
              />
            )}
          </FormField>
        </div>

        {/* AI Assist section */}
        {workspaceId && (
          <SmartCreateSection
            proposing={propose.isPending}
            proposalError={proposalError}
            proposal={hasProposal ? proposal : null}
            selectedAssignees={selectedAssignees}
            selectedSubtasks={selectedSubtasks}
            disabled={title.trim().length === 0 || submitting}
            applying={applySmartMutation.isPending}
            onPropose={handlePropose}
            onToggleAssignee={handleToggleAssignee}
            onToggleSubtask={handleToggleSubtask}
            onApply={() => {
              void handleApplySmart();
            }}
          />
        )}

        <FormActions hint={{ disabled: submitting || title.trim().length === 0 }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('tasks.form.cancel')}
          </Button>
          {/* Disable submit when the title is empty or whitespace-only so the
              user gets immediate UI feedback that a meaningful title is
              required. handleSubmit also re-validates via the Zod schema as a
              defence-in-depth check. */}
          <Button
            type="submit"
            variant="primary"
            disabled={submitting || title.trim().length === 0}
          >
            {t('tasks.form.submit')}
          </Button>
        </FormActions>
      </form>
    </Dialog>
  );
}

/* ── Internal presentational components ──────────────────────── */

interface AssigneeComboboxProps {
  id: string;
  workspaceId: string;
  selfId: string | undefined;
  selfName: string;
  value: string;
  onChange: (userId: string) => void;
}

/** Joined members only; the caller comes first under their own label. */
function AssigneeCombobox({
  id,
  workspaceId,
  selfId,
  selfName,
  value,
  onChange,
}: AssigneeComboboxProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const options = [
    ...(selfId
      ? [{ value: selfId, label: t('tasks.form.assignee_self', { name: selfName }) }]
      : []),
    ...members
      .filter((m) => m.joinedAt && m.userId !== selfId)
      .map((m) => ({ value: m.userId, label: m.displayName })),
  ];

  return <Combobox id={id} options={options} value={value} onChange={onChange} />;
}

interface SmartCreateSectionProps {
  proposing: boolean;
  proposalError: string | null;
  proposal: SmartProposal | null;
  selectedAssignees: Set<string>;
  selectedSubtasks: Set<number>;
  disabled: boolean;
  applying: boolean;
  onPropose: () => void;
  onToggleAssignee: (id: string) => void;
  onToggleSubtask: (idx: number) => void;
  onApply: () => void;
}

function SmartCreateSection({
  proposing,
  proposalError,
  proposal,
  selectedAssignees,
  selectedSubtasks,
  disabled,
  applying,
  onPropose,
  onToggleAssignee,
  onToggleSubtask,
  onApply,
}: SmartCreateSectionProps): ReactElement {
  const { t } = useTranslation('common');
  const hintId = useId();
  const proposalErrorText =
    proposalError === 'AI.EMBEDDING.DISABLED'
      ? t('AI.EMBEDDING.DISABLED', {
          ns: 'errors',
          defaultValue: t('tasks.smart_create.error'),
        })
      : proposalError === 'AI.EMBEDDING.NOT_CONFIGURED'
        ? t('AI.EMBEDDING.NOT_CONFIGURED', {
            ns: 'errors',
            defaultValue: t('tasks.smart_create.error'),
          })
        : proposalError === 'AI.PROVIDER.NOT_CONFIGURED'
          ? t('AI.PROVIDER.NOT_CONFIGURED', {
              ns: 'errors',
              defaultValue: t('tasks.smart_create.error'),
            })
          : t('tasks.smart_create.error');

  return (
    <section aria-label={t('tasks.smart_create.assist_button')} className={styles.smartSection}>
      <div key={proposing ? 'loading' : proposal ? 'result' : 'idle'} className={styles.smartStage}>
        {/* Propose button / loading */}
        {!proposal && !proposing && (
          <div className={styles.smartIntro}>
            <Button
              type="button"
              disabled={disabled}
              onClick={onPropose}
              aria-describedby={hintId}
              className={styles.smartTrigger}
            >
              <AiSparkle kind="suggests" />
              {t('tasks.smart_create.assist_button')}
            </Button>
            <p id={hintId} className={styles.smartHint}>
              {t('tasks.smart_create.assist_hint')}
            </p>
          </div>
        )}

        {proposing && (
          <div className={styles.smartLoading} role="status" aria-live="polite">
            <span className={styles.smartLoadingLabel}>{t('tasks.smart_create.suggesting')}</span>
            <Skeleton className={styles.smartSkeletonLine} />
            <Skeleton className={styles.smartSkeletonLineShort} />
          </div>
        )}

        {/* Error state */}
        {proposalError !== null && !proposing && (
          <p role="alert" className={styles.smartError}>
            {proposalErrorText}
          </p>
        )}

        {/* Proposal results */}
        {proposal && (
          <>
            {/* Suggested assignees */}
            {proposal.suggestedAssignees.length > 0 && (
              <fieldset className={styles.smartFieldset}>
                <legend className={styles.smartLegend}>
                  {t('tasks.smart_create.assignee_section')}
                </legend>
                <ul className={styles.smartList}>
                  {proposal.suggestedAssignees.map((a) => (
                    <AssigneeSuggestionRow
                      key={a.userPublicId}
                      assignee={a}
                      checked={selectedAssignees.has(a.userPublicId)}
                      onToggle={() => {
                        onToggleAssignee(a.userPublicId);
                      }}
                    />
                  ))}
                </ul>
              </fieldset>
            )}

            {/* Suggested subtasks */}
            {proposal.subtasks.length > 0 && (
              <fieldset className={styles.smartFieldset}>
                <legend className={styles.smartLegend}>
                  {t('tasks.smart_create.subtask_section')}
                </legend>
                <ul className={styles.smartList}>
                  {proposal.subtasks.map((st, idx) => (
                    <SubtaskProposalRow
                      key={`${st.title}-${String(idx)}`}
                      index={idx}
                      subtask={st}
                      checked={selectedSubtasks.has(idx)}
                      onToggle={() => {
                        onToggleSubtask(idx);
                      }}
                    />
                  ))}
                </ul>
              </fieldset>
            )}

            {/* No suggestions */}
            {proposal.suggestedAssignees.length === 0 && proposal.subtasks.length === 0 && (
              <p className={styles.smartEmpty}>{t('tasks.smart_create.no_suggestions')}</p>
            )}

            {/* Apply button */}
            {(proposal.suggestedAssignees.length > 0 || proposal.subtasks.length > 0) && (
              <div className={styles.smartApplyRow}>
                <Button type="button" variant="primary" disabled={applying} onClick={onApply}>
                  <AiSparkle kind="applies" />
                  {t('tasks.smart_create.apply')}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}

interface AssigneeSuggestionRowProps {
  assignee: AssigneeSuggestion;
  checked: boolean;
  onToggle: () => void;
}

function AssigneeSuggestionRow({
  assignee,
  checked,
  onToggle,
}: AssigneeSuggestionRowProps): ReactElement {
  const { t } = useTranslation('common');
  const confidence = Math.round(assignee.confidence * 100);
  const checkboxId = `assignee-${assignee.userPublicId}`;

  return (
    <li className={styles.assigneeRow}>
      <Checkbox id={checkboxId} checked={checked} onChange={onToggle} />
      <label htmlFor={checkboxId} className={styles.assigneeLabel}>
        <span className={styles.assigneeName}>{assignee.displayName}</span>
        <span className={styles.assigneeConfidence}>
          {t('tasks.smart_create.confidence', { value: String(confidence) })}
        </span>
      </label>
      <span className={styles.assigneeReason} title={assignee.reason}>
        {t('tasks.smart_create.reason', { reason: assignee.reason })}
      </span>
    </li>
  );
}

interface SubtaskProposalRowProps {
  index: number;
  subtask: SubtaskProposal;
  checked: boolean;
  onToggle: () => void;
}

function SubtaskProposalRow({
  index,
  subtask,
  checked,
  onToggle,
}: SubtaskProposalRowProps): ReactElement {
  const { t } = useTranslation('common');
  const baseId = useId();
  const checkboxId = `${baseId}-subtask-${index}`;
  const priorityLabel =
    subtask.priority === 'low'
      ? t('tasks.steps.priority_low')
      : subtask.priority === 'medium'
        ? t('tasks.steps.priority_medium')
        : subtask.priority === 'high'
          ? t('tasks.steps.priority_high')
          : subtask.priority === 'urgent'
            ? t('tasks.smart_create.priority_urgent')
            : t('tasks.smart_create.priority_unknown', { value: subtask.priority });

  return (
    <li className={styles.subtaskRow}>
      <Checkbox
        id={checkboxId}
        checked={checked}
        onChange={onToggle}
        className={styles.subtaskCheckbox}
      />
      <label htmlFor={checkboxId} className={styles.subtaskLabel}>
        <div className={styles.subtaskHeader}>
          <span className={styles.subtaskTitle}>{subtask.title}</span>
          <Badge tone={priorityTone(subtask.priority)}>{priorityLabel}</Badge>
        </div>
        {subtask.description && <p className={styles.subtaskDescription}>{subtask.description}</p>}
        {subtask.assignee && (
          <span className={styles.subtaskAssignee}>{subtask.assignee.displayName}</span>
        )}
      </label>
    </li>
  );
}

/** Map a string priority label to its numeric value. */
function priorityNumber(label: string): number {
  switch (label) {
    case 'low':
      return 1;
    case 'medium':
      return 2;
    case 'high':
      return 3;
    case 'urgent':
      return 4;
    default:
      return 0;
  }
}

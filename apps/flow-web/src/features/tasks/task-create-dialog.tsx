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
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { formatDate } from '../../lib/format';

import { TASK_PRIORITIES, type TaskPriority, useCreateTask } from './api';
import { PRIORITY_KEY } from './constants';
import {
  type AssigneeSuggestion,
  type SmartProposal,
  type SubtaskProposal,
  useApplySmartTask,
  useProposeSmartTask,
} from './smart-create-api';

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

const PRIORITY_TONE: Record<string, BadgeTone> = {
  low: 'info',
  medium: 'warning',
  high: 'danger',
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
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const create = useCreateTask();
  const propose = useProposeSmartTask();
  const applySmartMutation = useApplySmartTask();

  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState<TaskPriority>(2);
  const [dueOn, setDueOn] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  // AI Assist state
  const [proposal, setProposal] = useState<SmartProposal | null>(null);
  const [selectedAssignees, setSelectedAssignees] = useState<Set<string>>(new Set());
  const [selectedSubtasks, setSelectedSubtasks] = useState<Set<number>>(new Set());
  const [proposalError, setProposalError] = useState(false);

  const reset = (): void => {
    setTitle('');
    setDescription('');
    setPriority(2);
    setDueOn('');
    setErrors({});
    setProposal(null);
    setSelectedAssignees(new Set());
    setSelectedSubtasks(new Set());
    setProposalError(false);
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handlePropose = (): void => {
    if (!workspaceId || title.trim().length === 0) return;
    setProposalError(false);
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
        onError: () => {
          setProposalError(true);
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
    setSubmitting(true);
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
        assigneeUserIds: [...selectedAssignees],
        subtasks,
      });
      reset();
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.create_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const parsed = schema.safeParse({
      title,
      description: description.trim() === '' ? undefined : description,
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
    setSubmitting(true);
    try {
      await create.mutateAsync({
        projectId,
        title: parsed.data.title,
        ...(parsed.data.description ? { description: parsed.data.description } : {}),
        priority: parsed.data.priority as TaskPriority,
        ...(parsed.data.dueOn ? { dueOn: parsed.data.dueOn } : {}),
        visibility: 'public' as const,
      });
      reset();
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.create_failed') });
    } finally {
      setSubmitting(false);
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
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
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
              }}
              autoFocus
            />
          )}
        </FormField>

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

        <FormField label={t('tasks.form.priority')}>
          {(control) => (
            <Select
              {...control}
              value={String(priority)}
              onChange={(e) => {
                const next = Number.parseInt(e.target.value, 10) as TaskPriority;
                setPriority(next);
              }}
            >
              {TASK_PRIORITIES.map((p) => (
                <option key={p} value={String(p)}>
                  {t(PRIORITY_KEY[p])}
                </option>
              ))}
            </Select>
          )}
        </FormField>

        <FormField
          label={t('tasks.form.due')}
          {...(errors.dueOn ? { error: t(errors.dueOn) } : {})}
        >
          {() => (
            <DatePicker
              value={dueOn}
              onChange={setDueOn}
              weekdayLabels={weekdayLabels}
              formatMonthYear={formatMonthYear}
              prevLabel={t('calendar.prev')}
              nextLabel={t('calendar.next')}
              triggerLabel={dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder')}
            />
          )}
        </FormField>

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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('tasks.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('tasks.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/* ── Internal presentational components ──────────────────────── */

interface SmartCreateSectionProps {
  proposing: boolean;
  proposalError: boolean;
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

  return (
    <section
      aria-label={t('tasks.smart_create.assist_button')}
      style={{
        border: '1px solid var(--nf-color-border)',
        borderRadius: '0.5rem',
        padding: '1rem',
        display: 'flex',
        flexDirection: 'column',
        gap: '0.75rem',
      }}
    >
      {/* Propose button / loading */}
      {!proposal && !proposing && (
        <Button type="button" variant="ghost" disabled={disabled} onClick={onPropose}>
          {t('tasks.smart_create.assist_button')}
        </Button>
      )}

      {proposing && (
        <div
          style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}
          role="status"
          aria-live="polite"
        >
          <Spinner
            label={t('tasks.smart_create.suggesting')}
            style={{ inlineSize: '1rem', blockSize: '1rem' }}
          />
          <span>{t('tasks.smart_create.suggesting')}</span>
        </div>
      )}

      {/* Error state */}
      {proposalError && !proposing && (
        <p
          role="alert"
          style={{ color: 'var(--nf-color-danger)', margin: 0, fontSize: '0.875rem' }}
        >
          {t('tasks.smart_create.error')}
        </p>
      )}

      {/* Proposal results */}
      {proposal && (
        <>
          {/* Suggested assignees */}
          {proposal.suggestedAssignees.length > 0 && (
            <fieldset style={{ border: 'none', margin: 0, padding: 0 }}>
              <legend
                style={{
                  fontWeight: 600,
                  fontSize: '0.875rem',
                  marginBlockEnd: '0.5rem',
                }}
              >
                {t('tasks.smart_create.assignee_section')}
              </legend>
              <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
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
            <fieldset style={{ border: 'none', margin: 0, padding: 0 }}>
              <legend
                style={{
                  fontWeight: 600,
                  fontSize: '0.875rem',
                  marginBlockEnd: '0.5rem',
                }}
              >
                {t('tasks.smart_create.subtask_section')}
              </legend>
              <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
                {proposal.subtasks.map((st, idx) => (
                  <SubtaskProposalRow
                    key={`${st.title}-${String(idx)}`}
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
            <p style={{ margin: 0, fontSize: '0.875rem', color: 'var(--nf-color-fg-muted)' }}>
              {t('tasks.smart_create.no_suggestions')}
            </p>
          )}

          {/* Apply button */}
          {(proposal.suggestedAssignees.length > 0 || proposal.subtasks.length > 0) && (
            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <Button type="button" variant="primary" disabled={applying} onClick={onApply}>
                {t('tasks.smart_create.apply')}
              </Button>
            </div>
          )}
        </>
      )}
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
    <li
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        paddingBlock: '0.375rem',
      }}
    >
      <Checkbox id={checkboxId} checked={checked} onChange={onToggle} />
      <label htmlFor={checkboxId} style={{ flex: 1, cursor: 'pointer', fontSize: '0.875rem' }}>
        <span style={{ fontWeight: 500 }}>{assignee.displayName}</span>
        <span
          style={{
            marginInlineStart: '0.5rem',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.75rem',
          }}
        >
          {t('tasks.smart_create.confidence', { value: String(confidence) })}
        </span>
      </label>
      <span
        style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}
        title={assignee.reason}
      >
        {t('tasks.smart_create.reason', { reason: assignee.reason })}
      </span>
    </li>
  );
}

interface SubtaskProposalRowProps {
  subtask: SubtaskProposal;
  checked: boolean;
  onToggle: () => void;
}

function SubtaskProposalRow({ subtask, checked, onToggle }: SubtaskProposalRowProps): ReactElement {
  const checkboxId = `subtask-${subtask.title.replaceAll(/\s+/g, '-').slice(0, 32)}`;

  return (
    <li
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: '0.5rem',
        paddingBlock: '0.375rem',
      }}
    >
      <Checkbox
        id={checkboxId}
        checked={checked}
        onChange={onToggle}
        style={{ marginBlockStart: '0.125rem' }}
      />
      <label htmlFor={checkboxId} style={{ flex: 1, cursor: 'pointer', fontSize: '0.875rem' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.375rem' }}>
          <span style={{ fontWeight: 500 }}>{subtask.title}</span>
          <Badge tone={priorityTone(subtask.priority)}>{subtask.priority}</Badge>
        </div>
        {subtask.description && (
          <p
            style={{
              margin: '0.25rem 0 0',
              fontSize: '0.75rem',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {subtask.description}
          </p>
        )}
        {subtask.assignee && (
          <span
            style={{
              fontSize: '0.75rem',
              color: 'var(--nf-color-fg-muted)',
              marginBlockStart: '0.125rem',
              display: 'block',
            }}
          >
            {subtask.assignee.displayName}
          </span>
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

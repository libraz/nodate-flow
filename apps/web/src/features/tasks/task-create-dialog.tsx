/**
 * TaskCreateDialog — modal form to create a new task in a project.
 *
 * Assignee picker is deliberately omitted for now: the backend create
 * endpoint does not accept an assignee in the body, so we can only
 * attach actors via a follow-up POST /tasks/{id}/actors call. F8 will
 * introduce a real actor picker once that wiring is in place.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { type TaskPriority, useCreateTask } from './api';

export interface TaskCreateDialogProps {
  projectId: string;
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

const PRIORITIES: readonly TaskPriority[] = [0, 1, 2, 3, 4];

const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

export default function TaskCreateDialog({
  projectId,
  open,
  onClose,
}: TaskCreateDialogProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const create = useCreateTask();

  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState<TaskPriority>(2);
  const [dueOn, setDueOn] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setTitle('');
    setDescription('');
    setPriority(2);
    setDueOn('');
    setErrors({});
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
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
      });
      reset();
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.create_failed') });
    } finally {
      setSubmitting(false);
    }
  };

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
              {PRIORITIES.map((p) => (
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
          {(control) => (
            <Input
              {...control}
              type="date"
              lang={i18n.resolvedLanguage ?? 'en'}
              value={dueOn}
              onChange={(e) => {
                setDueOn(e.target.value);
              }}
            />
          )}
        </FormField>

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

/**
 * QuickCaptureDialog — lightweight one-form task-capture dialog.
 *
 * Exposes only a title (required) and an optional description —
 * priority, due date, and assignee stay in the full TaskCreateDialog.
 * The target project is resolved upstream by the caller
 * (`useDefaultProjectId`), so this dialog never renders a project
 * picker and never decides policy on its own.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { useCreateTask } from './api';
import { useTaskFormState } from './use-task-form-state';

export interface QuickCaptureDialogProps {
  /** Target project for the new task. */
  projectId: string;
  open: boolean;
  onClose: () => void;
}

/**
 * QuickCaptureDialog renders a minimal dialog for quick task entry.
 *
 * Submitting creates a task with only `title` and (optionally)
 * `description`, using the same `useCreateTask` mutation as the full
 * TaskCreateDialog so cache invalidation is consistent.
 */
export default function QuickCaptureDialog({
  projectId,
  open,
  onClose,
}: QuickCaptureDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const create = useCreateTask();

  const { title, description, titleError, setTitle, setDescription, setTitleError, reset } =
    useTaskFormState();
  const [submitting, setSubmitting] = useState(false);

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const trimmedTitle = title.trim();
    if (trimmedTitle.length === 0) {
      setTitleError(t('tasks.validation.title_required'));
      return;
    }
    setTitleError(null);
    setSubmitting(true);
    const trimmedDescription = description.trim();
    try {
      await create.mutateAsync({
        projectId,
        title: trimmedTitle,
        ...(trimmedDescription.length > 0 ? { description: trimmedDescription } : {}),
        priority: 2,
        visibility: 'public' as const,
      });
      reset();
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.create_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('tasks.quick_capture.title')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
      >
        <FormField
          label={t('tasks.form.title')}
          required
          {...(titleError ? { error: titleError } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={title}
              onChange={(e) => {
                setTitle(e.target.value);
              }}
              placeholder={t('tasks.title_placeholder')}
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
              rows={3}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--nf-space-3)' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('tasks.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('tasks.quick_capture.submit_label')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

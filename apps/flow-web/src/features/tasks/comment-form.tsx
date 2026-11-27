/**
 * CommentForm — the new-comment box on the task detail page.
 *
 * The comment is markdown, scanned for mentions on save exactly like a
 * description, so it uses the same editor (and `@` picker) rather than a
 * textarea of its own. Cmd/Ctrl+Enter submits.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import SubmitHint from '../../components/submit-hint';
import { formatApiError } from '../../lib/api-error';
import { onSubmitShortcut } from '../../lib/submit-shortcut';
import { useAddTaskComment } from './api';
import MarkdownEditor from './markdown-editor';

export interface CommentFormProps {
  taskId: string;
  /** Workspace whose members can be named with `@`. */
  workspaceId: string;
}

export default function CommentForm({ taskId, workspaceId }: CommentFormProps): ReactElement {
  const { t } = useTranslation('common');
  const add = useAddTaskComment();
  const [body, setBody] = useState('');

  const submit = async (): Promise<void> => {
    const trimmed = body.trim();
    if (trimmed.length === 0 || add.isPending) return;
    try {
      await add.mutateAsync({ taskId, body: trimmed });
      setBody('');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.comment_add_failed'),
      });
    }
  };

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    void submit();
  };

  return (
    <form
      onSubmit={handleSubmit}
      onKeyDown={onSubmitShortcut}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}
    >
      <FormField label={t('tasks.comments.title')}>
        {(control) => (
          <MarkdownEditor
            id={control.id}
            value={body}
            onChange={setBody}
            workspaceId={workspaceId}
            placeholder={t('tasks.comments.add_placeholder')}
            aria-label={t('tasks.comments.title')}
            rows={3}
          />
        )}
      </FormField>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'flex-end',
          gap: 'var(--nf-space-3)',
        }}
      >
        <SubmitHint />
        <Button type="submit" disabled={body.trim().length === 0 || add.isPending}>
          {t('tasks.comments.add')}
        </Button>
      </div>
    </form>
  );
}

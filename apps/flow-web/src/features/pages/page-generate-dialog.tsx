/**
 * PageGenerateDialog — modal that collects an LLM prompt for AI page
 * generation.
 *
 * The page editor owns the working `title` and `projectId`; this dialog
 * surfaces the title as read-only context, lets the user supply a free-form
 * `prompt`, and submits all three to {@link useGeneratePage}. On success the
 * generated body is handed back through `onGenerated` so the editor can
 * populate its own `body` state.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Sparkles } from 'lucide-react';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type PageItem, useGeneratePage } from './api';
import styles from './pages.module.css';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface PageGenerateDialogProps {
  /** Whether the dialog is open. */
  open: boolean;
  /** Workspace the generation request is scoped to. */
  workspaceId: string;
  /** Read-only title displayed as context for the user. */
  title: string;
  /** Optional project the generated page belongs to. */
  projectId: string | undefined;
  /** Called whether the user cancels or submission completes. */
  onClose: () => void;
  /** Invoked with the generated page on a successful response. */
  onGenerated: (generated: PageItem) => void;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function PageGenerateDialog({
  open,
  workspaceId,
  title,
  projectId,
  onClose,
  onGenerated,
}: PageGenerateDialogProps): ReactElement {
  const { t } = useTranslation('pages');
  const generateMutation = useGeneratePage(workspaceId);
  const isGenerating = generateMutation.isPending;

  const [prompt, setPrompt] = useState('');

  // Reset the prompt every time the dialog opens so each invocation starts
  // from a clean slate. We deliberately do not persist between sessions —
  // the title is the stable context, the prompt is one-shot intent.
  useEffect(() => {
    if (open) {
      setPrompt('');
    }
  }, [open]);

  const handleClose = (): void => {
    if (isGenerating) return;
    onClose();
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const trimmed = prompt.trim();
    if (trimmed.length === 0 || isGenerating) return;

    generateMutation.mutate(
      {
        title,
        prompt: trimmed,
        ...(projectId !== undefined && projectId.length > 0 ? { projectId } : {}),
      },
      {
        onSuccess: (generated) => {
          onGenerated(generated);
          onClose();
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('generate.error') });
        },
      },
    );
  };

  const submitDisabled = isGenerating || prompt.trim().length === 0;

  return (
    <Dialog open={open} onClose={handleClose} title={t('generate.dialog_title')} size="lg">
      <form onSubmit={handleSubmit} className={styles.generateForm}>
        <div className={styles.generateContext}>
          <span className={styles.generateContextLabel}>{t('generate.context_label')}</span>
          <span className={styles.generateContextValue}>{title}</span>
        </div>

        <FormField label={t('generate.prompt_label')} required>
          {(control) => (
            <Textarea
              {...control}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={t('generate.prompt_placeholder')}
              rows={6}
              autoFocus
              disabled={isGenerating}
            />
          )}
        </FormField>

        <p className={styles.generateHelp}>{t('generate.prompt_help')}</p>

        <div className={styles.generateActions}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleClose}
            disabled={isGenerating}
          >
            {t('generate.cancel')}
          </Button>
          <Button type="submit" variant="primary" size="sm" disabled={submitDisabled}>
            <Sparkles size={14} aria-hidden />
            {isGenerating ? t('generate.submitting') : t('generate.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

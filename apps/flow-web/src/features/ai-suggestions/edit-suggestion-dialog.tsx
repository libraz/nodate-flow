/**
 * EditSuggestionDialog — modal form for editing an AI triage suggestion's
 * `recommendedAction` and `reasoning` before applying. Local edits
 * only; the `ai.suggestion.edited` event is wired in a later polish.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import styles from './edit-suggestion-dialog.module.css';
import type { Suggestion } from './store';

const ACTIONS = ['open', 'snooze', 'archive'] as const;
type Action = (typeof ACTIONS)[number];

const editSchema = z.object({
  recommendedAction: z.enum(ACTIONS),
  reasoning: z.string().trim().min(1),
});

export type EditSuggestionPatch = z.infer<typeof editSchema>;

export interface EditSuggestionDialogProps {
  suggestion: Suggestion | null;
  open: boolean;
  onClose: () => void;
  onSave: (patch: EditSuggestionPatch) => void;
}

function isAction(value: string): value is Action {
  return (ACTIONS as readonly string[]).includes(value);
}

export default function EditSuggestionDialog({
  suggestion,
  open,
  onClose,
  onSave,
}: EditSuggestionDialogProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const [action, setAction] = useState<Action>('open');
  const [reasoning, setReasoning] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    if (suggestion) {
      const a = suggestion.recommendedAction;
      setAction(isAction(a) ? a : 'open');
      setReasoning(suggestion.reasoning);
    }
  }, [suggestion, open]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const result = editSchema.safeParse({ recommendedAction: action, reasoning });
    if (!result.success) {
      setError(t('edit.reasoning_required'));
      return;
    }
    onSave(result.data);
  };

  return (
    <Dialog open={open} onClose={onClose} title={t('edit.title')}>
      <form onSubmit={handleSubmit} className={styles.form}>
        <FormField label={t('edit.action_label')}>
          {(control) => (
            <Select
              {...control}
              value={action}
              onChange={(e) => {
                const v = e.target.value;
                if (isAction(v)) setAction(v);
              }}
            >
              <option value="open">{t('edit.action.open')}</option>
              <option value="snooze">{t('edit.action.snooze')}</option>
              <option value="archive">{t('edit.action.archive')}</option>
            </Select>
          )}
        </FormField>
        <FormField label={t('edit.reasoning_label')} error={error ?? undefined}>
          {(control) => (
            <Textarea
              {...control}
              value={reasoning}
              rows={4}
              onChange={(e) => setReasoning(e.target.value)}
            />
          )}
        </FormField>
        <div className={styles.actions}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            {t('edit.cancel')}
          </Button>
          <Button type="submit" variant="primary" size="sm">
            {t('edit.save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

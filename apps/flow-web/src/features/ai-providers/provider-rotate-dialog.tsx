/**
 * ProviderRotateDialog — modal form to rotate a provider's API key.
 *
 * Security mirrors ProviderAddDialog:
 * - The new key input is `type="password"` with `autocomplete="off"`.
 * - The local key state is cleared synchronously on success/close so no
 *   React state retains the plaintext.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useUpdateAiProvider } from './api';

export interface ProviderRotateDialogProps {
  workspaceId: string;
  providerId: string | null;
  open: boolean;
  onClose: () => void;
}

export default function ProviderRotateDialog({
  workspaceId,
  providerId,
  open,
  onClose,
}: ProviderRotateDialogProps): ReactElement {
  const { t } = useTranslation('ai');
  const update = useUpdateAiProvider(workspaceId);
  const [apiKey, setApiKey] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleClose = (): void => {
    setApiKey('');
    setSubmitting(false);
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (providerId === null || apiKey.length < 8) return;
    setSubmitting(true);
    try {
      await update.mutateAsync({ providerId, patch: { apiKey } });
      setApiKey('');
      toaster.show({ tone: 'success', message: t('providers.toast.rotated') });
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('providers.toast.error') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('providers.rotate_dialog.title')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
      >
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {t('providers.rotate_dialog.description')}
        </p>
        <FormField label={t('providers.field.api_key')} required>
          {(control) => (
            <Input
              {...control}
              type="password"
              autoComplete="off"
              spellCheck={false}
              placeholder={t('providers.field.api_key_placeholder')}
              value={apiKey}
              onChange={(e) => {
                setApiKey(e.target.value);
              }}
            />
          )}
        </FormField>
        <div style={{ display: 'flex', gap: 'var(--nf-space-2)', justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" onClick={handleClose}>
            {t('providers.action.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting || apiKey.length < 8}>
            {t('providers.rotate_dialog.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

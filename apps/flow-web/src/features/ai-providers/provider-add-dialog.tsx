/**
 * ProviderAddDialog — modal form to register an AI provider.
 *
 * Security:
 * - The API key input is `type="password"` with `autocomplete="off"`.
 * - There is no copy button and no display of the plaintext value after submit.
 * - On success, the local key state is cleared synchronously before the dialog
 *   closes, so no React state retains the plaintext.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type AiProviderKind, useCreateAiProvider } from './api';

const KINDS: readonly AiProviderKind[] = [
  'anthropic',
  'openai',
  'google',
  'ollama',
  'openai_compat',
] as const;

function kindLabelKey(kind: AiProviderKind): string {
  switch (kind) {
    case 'anthropic':
      return 'providers.kind.anthropic';
    case 'openai':
      return 'providers.kind.openai';
    case 'google':
      return 'providers.kind.google';
    case 'ollama':
      return 'providers.kind.ollama';
    case 'openai_compat':
      return 'providers.kind.openai_compat';
  }
}

export interface ProviderAddDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

export default function ProviderAddDialog({
  workspaceId,
  open,
  onClose,
}: ProviderAddDialogProps): ReactElement {
  const { t } = useTranslation('ai');
  const create = useCreateAiProvider(workspaceId);

  const [kind, setKind] = useState<AiProviderKind>('anthropic');
  const [name, setName] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [defaultModel, setDefaultModel] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setKind('anthropic');
    setName('');
    setApiKey('');
    setBaseUrl('');
    setDefaultModel('');
  };

  const handleClose = (): void => {
    // Clear plaintext from React state before closing.
    setApiKey('');
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (name.trim() === '' || apiKey.length < 8) return;
    setSubmitting(true);
    try {
      await create.mutateAsync({
        kind,
        name: name.trim(),
        apiKey,
        ...(baseUrl.trim() !== '' ? { baseUrl: baseUrl.trim() } : {}),
        ...(defaultModel.trim() !== '' ? { defaultModel: defaultModel.trim() } : {}),
      });
      // Clear plaintext immediately on success.
      setApiKey('');
      toaster.show({ tone: 'success', message: t('providers.toast.created') });
      reset();
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('providers.toast.error') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('providers.add')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
      >
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
          {t('providers.write_only_notice')}
        </p>

        <FormField label={t('providers.field.kind')} required>
          {(control) => (
            <Select
              {...control}
              value={kind}
              onChange={(e) => {
                setKind(e.target.value as AiProviderKind);
              }}
            >
              {KINDS.map((k) => (
                <option key={k} value={k}>
                  {t(kindLabelKey(k))}
                </option>
              ))}
            </Select>
          )}
        </FormField>

        <FormField label={t('providers.field.name')} required>
          {(control) => (
            <Input
              {...control}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          )}
        </FormField>

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

        <FormField label={t('providers.field.base_url')}>
          {(control) => (
            <Input
              {...control}
              value={baseUrl}
              onChange={(e) => {
                setBaseUrl(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField label={t('providers.field.default_model')}>
          {(control) => (
            <Input
              {...control}
              value={defaultModel}
              onChange={(e) => {
                setDefaultModel(e.target.value);
              }}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" onClick={handleClose}>
            {t('providers.action.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('providers.action.save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

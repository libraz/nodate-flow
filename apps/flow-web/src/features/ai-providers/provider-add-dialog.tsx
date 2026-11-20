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
import Radio from '@nodate-flow/ui/primitives/radio';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { type AiProviderKind, useCreateAiProvider } from './api';
import styles from './provider-add-dialog.module.css';

const KINDS: readonly AiProviderKind[] = [
  'anthropic',
  'openai',
  'google',
  'ollama',
  'openai_compat',
] as const;

/** One-line description of what each kind connects to. */
function kindHintKey(kind: AiProviderKind): string {
  switch (kind) {
    case 'anthropic':
      return 'providers.kind_hint.anthropic';
    case 'openai':
      return 'providers.kind_hint.openai';
    case 'google':
      return 'providers.kind_hint.google';
    case 'ollama':
      return 'providers.kind_hint.ollama';
    case 'openai_compat':
      return 'providers.kind_hint.openai_compat';
  }
}

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
  const kindGroupId = useId();
  // Local Ollama is unauthenticated; every other kind needs a key.
  const needsKey = kind !== 'ollama';

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
    if (name.trim() === '' || (needsKey && apiKey.length < 8)) return;
    setSubmitting(true);
    try {
      await create.mutateAsync({
        kind,
        name: name.trim(),
        ...(needsKey ? { apiKey } : {}),
        ...(baseUrl.trim() !== '' ? { baseUrl: baseUrl.trim() } : {}),
        ...(defaultModel.trim() !== '' ? { defaultModel: defaultModel.trim() } : {}),
      });
      // Clear plaintext immediately on success.
      setApiKey('');
      toaster.show({ tone: 'success', message: t('providers.toast.created') });
      reset();
      onClose();
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'providers.toast.error') });
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
        className={styles.form}
      >
        <p className={styles.notice}>{t('providers.write_only_notice')}</p>

        {/* Each kind names what it connects to, so the choice is made before picking. */}
        <fieldset className={styles.kindGroup} aria-describedby={`${kindGroupId}-help`}>
          <legend className={styles.kindLegend}>{t('providers.field.kind')}</legend>
          <p id={`${kindGroupId}-help`} className={styles.kindHelp}>
            {t('providers.field.kind_help')}
          </p>
          <div className={styles.kindOptions}>
            {KINDS.map((k) => {
              const optionId = `${kindGroupId}-${k}`;
              return (
                <label key={k} htmlFor={optionId} className={styles.kindOption}>
                  <Radio
                    id={optionId}
                    name={`${kindGroupId}-kind`}
                    value={k}
                    checked={kind === k}
                    onChange={() => {
                      setKind(k);
                      // The field is hidden for openai, so a value typed under
                      // another kind would otherwise be submitted invisibly.
                      if (k === 'openai') setBaseUrl('');
                      // Same for the key: it is hidden for ollama and must not ride along.
                      if (k === 'ollama') setApiKey('');
                    }}
                    aria-labelledby={`${optionId}-label`}
                    aria-describedby={`${optionId}-hint`}
                  />
                  <span className={styles.kindBody}>
                    <span id={`${optionId}-label`} className={styles.kindLabel}>
                      {t(kindLabelKey(k))}
                    </span>
                    <span id={`${optionId}-hint`} className={styles.kindHint}>
                      {t(kindHintKey(k))}
                    </span>
                  </span>
                </label>
              );
            })}
          </div>
        </fieldset>

        <FormField
          label={t('providers.field.name')}
          description={t('providers.field.name_help')}
          required
        >
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

        {needsKey ? (
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
        ) : (
          <p className={styles.notice}>{t('providers.ollama_no_key_note')}</p>
        )}

        {/*
          The openai kind is the official endpoint only; a custom base URL
          belongs to openai_compat. Offering the field here invited a
          configuration the server refuses to build — and, until that
          refusal moved to submit time, one that was stored and then broke
          every AI call in the workspace. Hiding it is the half that stops
          the mistake being made; the server check is the half that stops
          it being made another way.
        */}
        {kind !== 'openai' && (
          <FormField
            label={t('providers.field.base_url')}
            description={t('providers.field.base_url_description')}
          >
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
        )}

        <FormField
          label={t('providers.field.default_model')}
          description={t('providers.field.default_model_help')}
        >
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

        <div className={styles.actions}>
          <Button type="button" variant="ghost" onClick={handleClose}>
            {t('providers.action.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting} aria-busy={submitting}>
            {t('providers.action.save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

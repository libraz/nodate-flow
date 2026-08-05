/**
 * TokenCreateDialog — two-state dialog for creating an MCP token.
 *
 * State machine:
 *   form    — collect name and scopes, submit to create.
 *   reveal  — show plaintext token exactly once with a copy button.
 *
 * Security:
 * - The plaintext token lives ONLY in this component's local state.
 * - It is never written to a query cache, never logged, never persisted.
 * - On close (any path: backdrop, escape, Done button), `setPlaintext('')`
 *   runs synchronously before `onClose()` so React state retains nothing.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useCreateMcpToken } from './api';

export interface TokenCreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

type Stage = 'form' | 'reveal';

export default function TokenCreateDialog({
  workspaceId,
  open,
  onClose,
}: TokenCreateDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const create = useCreateMcpToken(workspaceId);

  const [stage, setStage] = useState<Stage>('form');
  const [name, setName] = useState('');
  const [scopesText, setScopesText] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [plaintext, setPlaintext] = useState('');
  const [copied, setCopied] = useState(false);

  const reset = (): void => {
    setStage('form');
    setName('');
    setScopesText('');
    setSubmitting(false);
    setCopied(false);
  };

  const handleClose = (): void => {
    // Synchronously clear plaintext from React state before closing.
    setPlaintext('');
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (name.trim() === '') return;
    setSubmitting(true);
    const scopes = scopesText
      .split(/\s+/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
    try {
      const result = await create.mutateAsync({
        name: name.trim(),
        scopes: scopes.length > 0 ? scopes : null,
      });
      setPlaintext(result.token);
      setStage('reveal');
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.mcp_tokens.errors.create_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = async (): Promise<void> => {
    if (plaintext === '') return;
    try {
      await navigator.clipboard.writeText(plaintext);
      setCopied(true);
    } catch {
      // Clipboard may be unavailable; ignore — user can still select manually.
    }
  };

  const title =
    stage === 'form'
      ? t('workspace.mcp_tokens.dialog.create_title')
      : t('workspace.mcp_tokens.dialog.reveal_title');

  return (
    <Dialog open={open} onClose={handleClose} title={title}>
      {stage === 'form' ? (
        <form
          onSubmit={(e) => {
            void handleSubmit(e);
          }}
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
        >
          <FormField label={t('workspace.mcp_tokens.dialog.field.name')} required>
            {(control) => (
              <Input
                {...control}
                value={name}
                placeholder={t('workspace.mcp_tokens.dialog.field.name_placeholder')}
                onChange={(e) => {
                  setName(e.target.value);
                }}
              />
            )}
          </FormField>

          <FormField
            label={t('workspace.mcp_tokens.dialog.field.scopes')}
            description={t('workspace.mcp_tokens.dialog.field.scopes_help')}
          >
            {(control) => (
              <Input
                {...control}
                value={scopesText}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => {
                  setScopesText(e.target.value);
                }}
              />
            )}
          </FormField>

          <div style={{ display: 'flex', gap: 'var(--nf-space-2)', justifyContent: 'flex-end' }}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('workspace.mcp_tokens.dialog.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={submitting || name.trim() === ''}>
              {t('workspace.mcp_tokens.dialog.submit')}
            </Button>
          </div>
        </form>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
          <p
            style={{
              margin: 0,
              color: 'var(--nf-color-warning-fg)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('workspace.mcp_tokens.dialog.reveal_warning')}
          </p>
          <FormField label={t('workspace.mcp_tokens.dialog.token_label')}>
            {(control) => (
              <Input
                {...control}
                value={plaintext}
                readOnly
                spellCheck={false}
                onFocus={(e) => {
                  e.currentTarget.select();
                }}
                style={{ fontFamily: 'var(--nf-font-mono, monospace)' }}
              />
            )}
          </FormField>
          <div style={{ display: 'flex', gap: 'var(--nf-space-2)', justifyContent: 'flex-end' }}>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                void handleCopy();
              }}
            >
              {copied
                ? t('workspace.mcp_tokens.dialog.copied')
                : t('workspace.mcp_tokens.dialog.copy')}
            </Button>
            <Button type="button" variant="primary" onClick={handleClose}>
              {t('workspace.mcp_tokens.dialog.done')}
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

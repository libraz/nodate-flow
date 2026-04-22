/**
 * RotateTokenDialog — shows the new plaintext share URL exactly once
 * after a successful rotate. Controlled by `share` prop: non-null opens
 * the dialog; `onClose` clears it. The new token is carried on the
 * response object and lives only in that parent state.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { PublicShare } from './api';

export interface RotateTokenDialogProps {
  share: PublicShare | null;
  webUrl: string;
  onClose: () => void;
}

export default function RotateTokenDialog({
  share,
  webUrl,
  onClose,
}: RotateTokenDialogProps): ReactElement | null {
  const { t } = useTranslation('settings');
  const [copied, setCopied] = useState(false);

  if (!share) return null;
  const token = typeof share.token === 'string' ? share.token : '';
  const url = token === '' ? '' : `${webUrl}/share/cal/${token}`;

  const handleCopy = async (): Promise<void> => {
    if (url === '') return;
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      // Clipboard unavailable; user can still select manually.
    }
  };

  const handleClose = (): void => {
    setCopied(false);
    onClose();
  };

  return (
    <Dialog
      open={share !== null}
      onClose={handleClose}
      title={t('workspace.public_shares.dialog.rotate_title')}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
        <p style={{ margin: 0, color: 'var(--nf-color-warning)', fontSize: '0.875rem' }}>
          {t('workspace.public_shares.dialog.rotate_warning')}
        </p>
        <FormField label={t('workspace.public_shares.dialog.url_label')}>
          {(control) => (
            <Input
              {...control}
              value={url}
              readOnly
              spellCheck={false}
              onFocus={(e) => {
                e.currentTarget.select();
              }}
              style={{ fontFamily: 'var(--font-mono, monospace)' }}
            />
          )}
        </FormField>
        <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              void handleCopy();
            }}
          >
            {copied
              ? t('workspace.public_shares.dialog.copied')
              : t('workspace.public_shares.dialog.copy')}
          </Button>
          <Button type="button" variant="primary" onClick={handleClose}>
            {t('workspace.public_shares.dialog.done')}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

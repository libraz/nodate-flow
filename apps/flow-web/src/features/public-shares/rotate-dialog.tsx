/**
 * RotateTokenDialog — shows the new plaintext share URL exactly once
 * after a successful rotate. Controlled by `share` prop: non-null opens
 * the dialog; `onClose` clears it. The new token is carried on the
 * response object and lives only in that parent state.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import SecretCallout from '../settings/secret-callout';
import type { PublicShareWithToken } from './api';
import styles from './rotate-dialog.module.css';

export interface RotateTokenDialogProps {
  share: PublicShareWithToken | null;
  webUrl: string;
  onClose: () => void;
}

export default function RotateTokenDialog({
  share,
  webUrl,
  onClose,
}: RotateTokenDialogProps): ReactElement | null {
  const { t } = useTranslation('settings');

  if (!share) return null;
  const token = typeof share.token === 'string' ? share.token : '';
  const url = token === '' ? '' : `${webUrl}/share/cal/${token}`;

  const handleClose = (): void => {
    onClose();
  };

  return (
    <Dialog
      open={share !== null}
      onClose={handleClose}
      title={t('workspace.public_shares.dialog.rotate_title')}
    >
      <div className={styles.body}>
        {/* A failed copy is left silent: the field stays selectable for a manual copy. */}
        <SecretCallout
          kind="once"
          message={t('workspace.public_shares.dialog.rotate_warning')}
          label={t('workspace.public_shares.dialog.url_label')}
          value={url}
          copyLabel={t('workspace.public_shares.dialog.copy')}
          copiedLabel={t('workspace.public_shares.dialog.copied')}
        />
        <div className={styles.endActions}>
          <Button type="button" variant="primary" onClick={handleClose}>
            {t('workspace.public_shares.dialog.done')}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

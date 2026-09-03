/**
 * PublicShareCreateDialog — two-state dialog for creating a public share page.
 *
 * State machine:
 *   form    — collect title, description, optional holidays country/timezone.
 *   reveal  — show the plaintext URL exactly once with a copy button.
 *
 * Security: the plaintext token is kept only in this component's local
 * state, cleared synchronously on close, and never written to the query
 * cache. The server returns the plaintext only at create time; subsequent
 * GETs omit the `token` field.
 */

import { SUPPORTED_COUNTRIES } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import { type CreatePublicShareInput, useCreatePublicShare } from './api';
import styles from './create-dialog.module.css';

export interface PublicShareCreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

type Stage = 'form' | 'reveal';

export default function PublicShareCreateDialog({
  workspaceId,
  open,
  onClose,
}: PublicShareCreateDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const create = useCreatePublicShare(workspaceId);

  const [stage, setStage] = useState<Stage>('form');
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [holidaysCountry, setHolidaysCountry] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [plaintext, setPlaintext] = useState('');
  const [copied, setCopied] = useState(false);

  const shareUrl = plaintext === '' ? '' : `${getPublicBaseUrl()}/share/cal/${plaintext}`;

  const reset = (): void => {
    setStage('form');
    setTitle('');
    setDescription('');
    setHolidaysCountry('');
    setSubmitting(false);
    setCopied(false);
  };

  const handleClose = (): void => {
    setPlaintext('');
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (title.trim() === '') return;
    setSubmitting(true);
    const body: CreatePublicShareInput = { title: title.trim() };
    if (description.trim() !== '') body.description = description.trim();
    if (holidaysCountry !== '') body.showHolidaysCountry = holidaysCountry;
    try {
      const result = await create.mutateAsync(body);
      if (typeof result.token === 'string' && result.token !== '') {
        setPlaintext(result.token);
        setStage('reveal');
      } else {
        toaster.show({
          tone: 'danger',
          message: t('workspace.public_shares.errors.create_failed'),
        });
      }
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.public_shares.errors.create_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = async (): Promise<void> => {
    if (shareUrl === '') return;
    try {
      await navigator.clipboard.writeText(shareUrl);
      setCopied(true);
    } catch {
      // Clipboard may be unavailable; user can still select manually.
    }
  };

  const dialogTitle =
    stage === 'form'
      ? t('workspace.public_shares.dialog.create_title')
      : t('workspace.public_shares.dialog.reveal_title');

  return (
    <Dialog open={open} onClose={handleClose} title={dialogTitle}>
      {stage === 'form' ? (
        <form
          onSubmit={(e) => {
            void handleSubmit(e);
          }}
          className={styles.form}
        >
          <FormField label={t('workspace.public_shares.dialog.field.title')} required>
            {(control) => (
              <Input
                {...control}
                value={title}
                placeholder={t('workspace.public_shares.dialog.field.title_placeholder')}
                onChange={(e) => {
                  setTitle(e.target.value);
                }}
              />
            )}
          </FormField>

          <FormField
            label={t('workspace.public_shares.dialog.field.description')}
            description={t('workspace.public_shares.dialog.field.description_help')}
          >
            {(control) => (
              <Input
                {...control}
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                }}
              />
            )}
          </FormField>

          <FormField
            label={t('workspace.public_shares.dialog.field.holidays_country')}
            description={t('workspace.public_shares.dialog.field.holidays_country_help')}
          >
            {(control) => (
              <select
                {...control}
                value={holidaysCountry}
                onChange={(e) => {
                  setHolidaysCountry(e.target.value);
                }}
                className={styles.select}
              >
                <option value="">
                  {t('workspace.public_shares.dialog.field.holidays_country_unset')}
                </option>
                {Object.entries(SUPPORTED_COUNTRIES)
                  .sort(([, a], [, b]) => a.localeCompare(b))
                  .map(([code, name]) => (
                    <option key={code} value={code}>
                      {code} — {name}
                    </option>
                  ))}
              </select>
            )}
          </FormField>

          <div className={styles.endActions}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('workspace.public_shares.dialog.cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              data-testid="public-share-create-submit"
              disabled={submitting || title.trim() === ''}
            >
              {t('workspace.public_shares.dialog.submit')}
            </Button>
          </div>
        </form>
      ) : (
        <div className={styles.reveal}>
          <p className={styles.warning}>{t('workspace.public_shares.dialog.reveal_warning')}</p>
          <FormField label={t('workspace.public_shares.dialog.url_label')}>
            {(control) => (
              <Input
                {...control}
                value={shareUrl}
                readOnly
                spellCheck={false}
                onFocus={(e) => {
                  e.currentTarget.select();
                }}
                className={styles.urlInput}
              />
            )}
          </FormField>
          <div className={styles.endActions}>
            <Button
              type="button"
              variant="ghost"
              data-testid="public-share-create-copy"
              onClick={() => {
                void handleCopy();
              }}
            >
              {copied
                ? t('workspace.public_shares.dialog.copied')
                : t('workspace.public_shares.dialog.copy')}
            </Button>
            <Button
              type="button"
              variant="primary"
              data-testid="public-share-create-done"
              onClick={handleClose}
            >
              {t('workspace.public_shares.dialog.done')}
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

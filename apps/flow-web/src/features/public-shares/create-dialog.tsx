/**
 * PublicShareCreateDialog — two-state dialog for creating a public share page.
 *
 * State machine:
 *   form    — collect title, description, optional holidays country, optional expiry.
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
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import SecretCallout from '../settings/secret-callout';
import { type CreatePublicShareInput, useCreatePublicShare } from './api';
import styles from './create-dialog.module.css';
import ExpiryField, { draftToEpoch, emptyExpiryDraft } from './expiry-field';

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
  const [expiry, setExpiry] = useState(() => emptyExpiryDraft());
  const [expiryError, setExpiryError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [plaintext, setPlaintext] = useState('');

  const shareUrl = plaintext === '' ? '' : `${getPublicBaseUrl()}/share/cal/${plaintext}`;

  const reset = (): void => {
    setStage('form');
    setTitle('');
    setDescription('');
    setHolidaysCountry('');
    setExpiry(emptyExpiryDraft());
    setExpiryError('');
    setSubmitting(false);
  };

  const handleClose = (): void => {
    setPlaintext('');
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (title.trim() === '') return;
    const expiresAt = draftToEpoch(expiry);
    if (expiresAt !== undefined && !(expiresAt > Math.floor(Date.now() / 1000))) {
      setExpiryError(t('workspace.public_shares.expiry.error_past'));
      return;
    }
    setExpiryError('');
    setSubmitting(true);
    const body: CreatePublicShareInput = { title: title.trim() };
    if (description.trim() !== '') body.description = description.trim();
    if (holidaysCountry !== '') body.showHolidaysCountry = holidaysCountry;
    if (expiresAt !== undefined) body.expiresAt = expiresAt;
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
          <FormField
            label={t('workspace.public_shares.dialog.field.title')}
            description={t('workspace.public_shares.dialog.field.title_help')}
            required
          >
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
              <Select
                {...control}
                value={holidaysCountry}
                onChange={(e) => {
                  setHolidaysCountry(e.target.value);
                }}
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
              </Select>
            )}
          </FormField>

          <ExpiryField
            draft={expiry}
            onChange={(next) => {
              setExpiry(next);
              setExpiryError('');
            }}
            error={expiryError === '' ? undefined : expiryError}
          />

          <div className={styles.endActions}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('workspace.public_shares.dialog.cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              data-testid="public-share-create-submit"
              disabled={submitting || title.trim() === ''}
              aria-busy={submitting || undefined}
            >
              {t('workspace.public_shares.dialog.submit')}
            </Button>
          </div>
        </form>
      ) : (
        <div className={styles.reveal}>
          {/* A failed copy is left silent: the field stays selectable for a manual copy. */}
          <SecretCallout
            kind="once"
            message={t('workspace.public_shares.dialog.reveal_warning')}
            label={t('workspace.public_shares.dialog.url_label')}
            value={shareUrl}
            copyLabel={t('workspace.public_shares.dialog.copy')}
            copiedLabel={t('workspace.public_shares.dialog.copied')}
            copyTestId="public-share-create-copy"
          />
          <div className={styles.endActions}>
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

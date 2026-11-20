/**
 * SecretCallout: the one shape for revealing a token, URL or signing
 * secret. A key icon and a short heading say what to do; `once` says the
 * value cannot be shown again, `keep` that it can be fetched later but
 * must stay private. The value sits in a read-only field with a copy action.
 */

import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { KeyRound } from 'lucide-react';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import CopyButton from './copy-button';
import ui from './settings-ui.module.css';

export interface SecretCalloutProps {
  kind: 'once' | 'keep';
  /** Feature-specific consequence, e.g. how to recover a lost value. */
  message: string;
  label: string;
  value: string;
  copyLabel: string;
  copiedLabel: string;
  onCopyError?: (err: unknown) => void;
  /** `data-testid` on the copy button. */
  copyTestId?: string;
}

export default function SecretCallout({
  kind,
  message,
  label,
  value,
  copyLabel,
  copiedLabel,
  onCopyError,
  copyTestId,
}: SecretCalloutProps): ReactElement {
  const { t } = useTranslation('settings');
  const titleId = useId();
  return (
    <section className={ui.secretPanel} aria-labelledby={titleId}>
      <div className={ui.secretHead}>
        <KeyRound size={16} aria-hidden="true" />
        <h3 id={titleId} className={ui.secretTitle}>
          {kind === 'once' ? t('secret_callout.once_title') : t('secret_callout.keep_title')}
        </h3>
      </div>
      <p className={ui.secretWarning}>{message}</p>
      <FormField label={label}>
        {(control) => (
          <div className={ui.secretRow}>
            <Input
              {...control}
              className={ui.secretValue}
              value={value}
              readOnly
              spellCheck={false}
              autoComplete="off"
              onFocus={(e) => {
                e.currentTarget.select();
              }}
            />
            <CopyButton
              value={value}
              label={copyLabel}
              copiedLabel={copiedLabel}
              {...(onCopyError ? { onError: onCopyError } : {})}
              {...(copyTestId ? { testId: copyTestId } : {})}
            />
          </div>
        )}
      </FormField>
    </section>
  );
}

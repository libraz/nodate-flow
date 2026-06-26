/**
 * MaskedKey — renders a masked API key (`prefix***suffix`) in monospace with a
 * lock icon. The plaintext key never reaches this component.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface MaskedKeyProps {
  value: string;
}

export default function MaskedKey({ value }: MaskedKeyProps): ReactElement {
  const { t } = useTranslation('ai');
  return (
    <span
      role="img"
      aria-label={t('providers.masked_key_label')}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.375rem',
        fontFamily: 'var(--font-mono, ui-monospace, monospace)',
        fontSize: 'var(--nf-text-sm)',
        color: 'var(--nf-color-fg-muted)',
      }}
    >
      <span aria-hidden="true">🔒</span>
      <code>{value}</code>
    </span>
  );
}

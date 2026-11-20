/**
 * MaskedKey — renders a masked API key (`prefix***suffix`) in monospace with a
 * lock icon. The plaintext key never reaches this component.
 */

import Icon from '@nodate-flow/ui/icon';
import { Lock } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface MaskedKeyProps {
  /** Absent when the provider has no key (local Ollama). */
  value?: string | undefined;
}

export default function MaskedKey({ value }: MaskedKeyProps): ReactElement {
  const { t } = useTranslation('ai');
  if (value === undefined || value === '') {
    return (
      <span
        style={{
          fontSize: 'var(--nf-text-sm)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {t('providers.no_key')}
      </span>
    );
  }
  return (
    <span
      role="img"
      aria-label={t('providers.masked_key_label')}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--nf-space-1-5)',
        fontFamily: 'var(--nf-font-mono, ui-monospace, monospace)',
        fontSize: 'var(--nf-text-sm)',
        color: 'var(--nf-color-fg-muted)',
      }}
    >
      <Icon icon={Lock} decorative size={14} />
      <code>{value}</code>
    </span>
  );
}

/**
 * MaskedKey — renders a masked API key (`prefix***suffix`) in monospace with a
 * lock icon. The plaintext key never reaches this component.
 */

import type { ReactElement } from 'react';

export interface MaskedKeyProps {
  value: string;
}

export default function MaskedKey({ value }: MaskedKeyProps): ReactElement {
  return (
    <span
      aria-label="API key (masked)"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.375rem',
        fontFamily: 'var(--font-mono, ui-monospace, monospace)',
        fontSize: '0.875rem',
        color: 'var(--color-muted)',
      }}
    >
      <span aria-hidden="true">🔒</span>
      <code>{value}</code>
    </span>
  );
}

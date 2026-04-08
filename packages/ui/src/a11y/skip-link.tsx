/**
 * SkipLink — keyboard-accessible "skip to main content" link.
 *
 * Visually hidden until focused, then becomes visible in the top-inline-start corner.
 * Always pair with a target element that has a matching `id` and `tabIndex={-1}`.
 */

import type { CSSProperties, JSX, ReactNode } from 'react';

export interface SkipLinkProps {
  /** Fragment id to jump to (e.g. `"main"`). The leading `#` is added automatically. */
  targetId: string;
  /** Already-translated label. The caller is responsible for i18n. */
  children: ReactNode;
}

const STYLE: CSSProperties = {
  position: 'absolute',
  insetInlineStart: 'var(--nf-space-2)',
  insetBlockStart: 'var(--nf-space-2)',
  paddingBlock: 'var(--nf-space-2)',
  paddingInline: 'var(--nf-space-3)',
  background: 'var(--nf-color-bg-elevated)',
  color: 'var(--nf-color-fg)',
  border: 'var(--nf-space-px) solid var(--nf-color-border-strong)',
  borderRadius: 'var(--nf-radius-sm)',
  fontFamily: 'var(--nf-font-sans)',
  fontSize: 'var(--nf-text-sm)',
  zIndex: 1600,
  transform: 'translateY(-200%)',
  transition: 'transform var(--nf-duration-fast) var(--nf-ease-standard)',
};

export default function SkipLink({ targetId, children }: SkipLinkProps): JSX.Element {
  return (
    <a
      href={`#${targetId}`}
      style={STYLE}
      onFocus={(e) => {
        e.currentTarget.style.transform = 'translateY(0)';
      }}
      onBlur={(e) => {
        e.currentTarget.style.transform = 'translateY(-200%)';
      }}
    >
      {children}
    </a>
  );
}

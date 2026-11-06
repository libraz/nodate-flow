/**
 * Stroke icons for the account-shell navigation. Decorative only: every
 * use sits next to a visible label. Drawn on a 24-unit grid and sized by
 * the surrounding CSS (`currentColor` stroke).
 */

import type { ReactElement, ReactNode } from 'react';

function Glyph({ children }: { children: ReactNode }): ReactElement {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

export function ProfileIcon(): ReactElement {
  return (
    <Glyph>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 20c0-3.3 3.6-6 8-6s8 2.7 8 6" />
    </Glyph>
  );
}

export function SecurityIcon(): ReactElement {
  return (
    <Glyph>
      <path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6L12 3Z" />
      <path d="m9 12 2 2 4-4" />
    </Glyph>
  );
}

export function WorkspacesIcon(): ReactElement {
  return (
    <Glyph>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </Glyph>
  );
}

export function AdminIcon(): ReactElement {
  return (
    <Glyph>
      <path d="M4 7h10M18 7h2M4 17h2M10 17h10" />
      <circle cx="16" cy="7" r="2" />
      <circle cx="8" cy="17" r="2" />
    </Glyph>
  );
}

export function BackIcon(): ReactElement {
  return (
    <Glyph>
      <path d="M19 12H5M11 18l-6-6 6-6" />
    </Glyph>
  );
}

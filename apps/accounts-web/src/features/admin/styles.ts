/**
 * Shared inline-style objects for the admin pages.
 *
 * Centralizes the table / description-list / badge surface styling that was
 * duplicated across five admin routes (admins, audit-logs, users,
 * users/$userId, workspaces, workspaces/$wsId). Every value resolves through
 * the design-token cascade (`var(--nf-*)`) so theme switching and
 * design-token migrations stay zero-code.
 *
 * The accounts-web app already exposes equivalent CSS classes in
 * `src/styles/layout.css` (`aw-table`, `aw-th`, `aw-td`, `aw-badge*`); this
 * module is the inline-style sibling for routes that still pass through
 * `style={{ ... }}` props. New code should prefer the CSS classes.
 */

import type { CSSProperties } from 'react';

/** Width / collapse / size baseline for `<table>` rendered in admin lists. */
export const adminTableStyle: CSSProperties = {
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: 'var(--nf-text-sm)',
};

/** Header cell baseline for the admin list tables. */
export const adminThStyle: CSSProperties = {
  textAlign: 'start',
  padding: 'var(--nf-space-2) var(--nf-space-3)',
  borderBlockEnd: '2px solid var(--nf-color-border)',
  fontWeight: 600,
  color: 'var(--nf-color-fg-muted)',
  whiteSpace: 'nowrap',
};

/** Body cell baseline for the admin list tables. */
export const adminTdStyle: CSSProperties = {
  padding: 'var(--nf-space-2) var(--nf-space-3)',
  borderBlockEnd: '1px solid var(--nf-color-border)',
};

/** Description-list label (small muted caption) used on detail pages. */
export const adminLabelStyle: CSSProperties = {
  color: 'var(--nf-color-fg-muted)',
  fontSize: 'var(--nf-text-xs)',
  marginBlockEnd: 'var(--nf-space-1)',
};

/** Description-list value (regular body line) used on detail pages. */
export const adminValueStyle: CSSProperties = {
  fontSize: 'var(--nf-text-sm)',
  marginBlockEnd: 'var(--nf-space-3)',
};

/**
 * Pill-shaped badge baseline used for "enabled" / "suspended" status chips.
 * Callers spread this and add `background` / `color` based on the status.
 */
export const adminBadgeBase: CSSProperties = {
  display: 'inline-block',
  paddingBlock: 'var(--nf-space-1)',
  paddingInline: 'var(--nf-space-2)',
  borderRadius: 'var(--nf-radius-pill)',
  fontSize: 'var(--nf-text-xs)',
  fontWeight: 500,
};

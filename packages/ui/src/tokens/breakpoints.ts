/**
 * Breakpoint constants for responsive design.
 *
 * CSS custom properties cannot be used inside `@media` queries, so
 * breakpoints are exported as plain JS/TS constants. The values match
 * common Tailwind defaults and are also documented in `base.css`.
 *
 * @example
 * ```ts
 * import { BP } from '@nodate-flow/ui/tokens/breakpoints';
 * const isMobile = window.matchMedia(`(max-width: ${BP.sm - 1}px)`).matches;
 * ```
 */

/** Breakpoint pixel values. */
export const BP = {
  /** Small: 640px (landscape phones, small tablets) */
  sm: 640,
  /** Medium: 768px (tablets) */
  md: 768,
  /** Large: 1024px (small desktops, landscape tablets) */
  lg: 1024,
  /** Extra large: 1280px (desktops) */
  xl: 1280,
} as const;

/** Media query strings ready for `window.matchMedia()` or CSS-in-JS. */
export const MQ = {
  sm: `(min-width: ${BP.sm}px)`,
  md: `(min-width: ${BP.md}px)`,
  lg: `(min-width: ${BP.lg}px)`,
  xl: `(min-width: ${BP.xl}px)`,
} as const;

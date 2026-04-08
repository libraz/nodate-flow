/**
 * @nodate-flow/ui — design system entry point.
 *
 * Most consumers should import from subpath exports (`@nodate-flow/ui/icon`,
 * `@nodate-flow/ui/hooks/use-theme`, etc.) for tree-shaking. The barrel below
 * re-exports the stable, type-only surface.
 */

export { default as Icon } from './icon';
export type { IconProps } from './icon';

export { useTheme, THEME_IDS } from './hooks/use-theme';
export type { ThemeId, UseThemeOptions, UseThemeResult } from './hooks/use-theme';

export { useControllableState } from './hooks/use-controllable-state';
export { useFocusTrap } from './hooks/use-focus-trap';

export { default as SkipLink } from './a11y/skip-link';
export { default as VisuallyHidden } from './a11y/visually-hidden';
export { default as LiveRegion } from './a11y/live-region';

export { cx, clsx } from './lib/cx';
export type { ClassValue } from './lib/cx';

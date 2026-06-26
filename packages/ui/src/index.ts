/**
 * @nodate-flow/ui — design system entry point.
 *
 * Most consumers should import from subpath exports (`@nodate-flow/ui/icon`,
 * `@nodate-flow/ui/hooks/use-theme`, etc.) for tree-shaking. The barrel below
 * re-exports the stable, type-only surface.
 */

export { default as LiveRegion } from './a11y/live-region';
export { default as SkipLink } from './a11y/skip-link';
export { default as VisuallyHidden } from './a11y/visually-hidden';
export { useControllableState } from './hooks/use-controllable-state';
export { useFocusTrap } from './hooks/use-focus-trap';
export type { ThemeId, UseThemeOptions, UseThemeResult } from './hooks/use-theme';
export { THEME_IDS, useTheme } from './hooks/use-theme';
export type {
  ApiFieldError,
  UseZodFormOptions,
  UseZodFormReturn,
} from './hooks/use-zod-form';
export { useZodForm } from './hooks/use-zod-form';
export type { IconProps } from './icon';
export { default as Icon } from './icon';
export type { ClassValue } from './lib/cx';
export { clsx, cx } from './lib/cx';

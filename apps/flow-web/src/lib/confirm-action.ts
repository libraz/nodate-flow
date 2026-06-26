/**
 * confirmAction — re-export of the shared themed-confirm helper.
 *
 * Locale-aware defaults are registered once at app boot in
 * `providers/i18n-provider`. Imports from this path remain stable to avoid
 * touching every call site.
 */

export {
  type ConfirmActionOptions,
  confirmAction,
} from '@nodate-flow/ui/primitives/confirm/action';

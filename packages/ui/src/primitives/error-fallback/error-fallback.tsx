/**
 * ErrorFallback — section-level error placeholder primitive.
 *
 * Designed for per-section degradation (see commit 5bf9eca where the
 * codebase moved from a single root-level fallback to per-section
 * fallbacks). The component is sized to fit inside a card, list, or
 * collapsible section body — it is NOT a full-page error surface.
 *
 * Renders:
 *   - `role="alert"` so screen readers announce the failure.
 *   - A short, already-translated `title` (required).
 *   - Optional `description` for additional context.
 *   - Optional retry button driven by `action.label` + `action.onClick`.
 *   - A visually-hidden span carrying `error.message` for diagnostics,
 *     mirroring the existing pattern in feature error fallbacks.
 *
 * The primitive is i18n-agnostic — consumers pass already-translated
 * strings or ReactNodes via `title` / `description` / `action.label`.
 *
 * Tones:
 *   - `card` (default) — border + padding, suitable for standalone
 *     section bodies.
 *   - `inline` — flush, no border, suitable for tight containers
 *     (e.g. inside an already-bordered card).
 */

import type { HTMLAttributes, ReactElement, ReactNode } from 'react';
import { cx } from '../../lib/cx';
import Button from '../button/button';
import styles from './error-fallback.module.css';

export type ErrorFallbackTone = 'card' | 'inline';

export interface ErrorFallbackAction {
  /** Already-translated label (typically the i18n value of `error.retry`). */
  label: ReactNode;
  /** Click handler — usually `resetErrorBoundary` or a refetch trigger. */
  onClick: () => void;
}

export interface ErrorFallbackProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  /** Required, already-translated headline (e.g. "Could not load tasks"). */
  title: ReactNode;
  /** Optional secondary copy. */
  description?: ReactNode;
  /** Optional retry CTA. Omit for read-only fallbacks. */
  action?: ErrorFallbackAction;
  /**
   * Original error. Surfaced inside a hidden span (`<span hidden>`) so
   * the message is reachable for diagnostics without being announced
   * to users. Accepts `unknown` so consumers can pass raw catch values.
   */
  error?: unknown;
  /** Visual tone. Defaults to `"card"`. */
  tone?: ErrorFallbackTone;
}

/**
 * Convert an unknown thrown value into a string suitable for the
 * hidden diagnostic span. Mirrors the pattern used by the existing
 * feature-level error fallbacks.
 */
function readErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  if (error == null) return '';
  // Best-effort fallback; never throws because String() handles all values.
  return String((error as { message?: unknown })?.message ?? '');
}

/**
 * ErrorFallback renders a section-level error placeholder with optional
 * retry CTA and a hidden diagnostic span carrying the raw error message.
 */
export default function ErrorFallback({
  title,
  description,
  action,
  error,
  tone = 'card',
  className,
  ...rest
}: ErrorFallbackProps): ReactElement {
  return (
    <div
      role="alert"
      className={cx(
        styles.root,
        tone === 'card' && styles.card,
        tone === 'inline' && styles.inline,
        className,
      )}
      {...rest}
    >
      <p className={styles.title}>{title}</p>
      {description ? <p className={styles.description}>{description}</p> : null}
      {action ? (
        <div className={styles.actions}>
          <Button type="button" variant="ghost" size="sm" onClick={action.onClick}>
            {action.label}
          </Button>
        </div>
      ) : null}
      {/*
       * The raw message is intentionally kept off-screen. The translated
       * copy above is enough for users; the SDK forwards the structured
       * error to the route-level boundary for full diagnostics.
       */}
      <span hidden>{readErrorMessage(error)}</span>
    </div>
  );
}

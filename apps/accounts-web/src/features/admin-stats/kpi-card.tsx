/**
 * KpiCard — single counter tile for the instance-stats dashboard.
 *
 * Renders a label, a large tabular-numeric value, and a one-line
 * help description. Loading state shows a placeholder dash so the
 * layout doesn't jump when data resolves.
 */

import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface KpiCardProps {
  /** Short label rendered above the value. */
  title: string;
  /** Numeric counter; rendered as `—` when undefined. */
  value: number | undefined;
  /** Help text rendered below the value. */
  help: string;
  /** True while the underlying query is pending. */
  loading: boolean;
}

/**
 * Formats a counter using the user's locale grouping (e.g. `12,345`).
 * Falls back to a plain dash when the value is missing.
 */
function formatValue(value: number | undefined, locale: string): string {
  if (value === undefined) return '—';
  return new Intl.NumberFormat(locale).format(value);
}

/** Single-tile KPI card. */
function KpiCard({ title, value, help, loading }: KpiCardProps): ReactElement {
  // The grouping separator follows the language the product is set to,
  // not the one the OS is set to. A reader who chose Japanese and is on
  // an en-US machine was getting US grouping in the middle of a Japanese
  // page — the same split that made every admin timestamp US-shaped.
  const { i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? 'en';
  const display = loading && value === undefined ? '—' : formatValue(value, locale);

  return (
    <Card
      elevated
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
        padding: 'var(--nf-space-6)',
        // nf-token-override: component dimension, not a spacing step
        minBlockSize: '160px',
      }}
    >
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-sm)',
          fontWeight: 600,
          color: 'var(--nf-color-fg-muted)',
          letterSpacing: '0.02em',
          textTransform: 'uppercase',
        }}
      >
        {title}
      </p>
      <p
        aria-live="polite"
        aria-busy={loading || undefined}
        style={{
          margin: 0,
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-4xl)',
          fontWeight: 700,
          fontVariantNumeric: 'tabular-nums',
          color: 'var(--nf-color-fg)',
          lineHeight: 1.1,
        }}
      >
        {display}
      </p>
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-xs)',
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {help}
      </p>
    </Card>
  );
}

export default KpiCard;

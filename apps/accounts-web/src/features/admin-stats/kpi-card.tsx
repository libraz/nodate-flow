/**
 * KpiCard — single counter tile for the instance-stats dashboard.
 *
 * Renders a label, a large tabular-numeric value, and a one-line
 * help description. The first load holds the value's line with a
 * skeleton so the tile does not grow when data resolves.
 */

import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './kpi-card.module.css';

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
  const display = formatValue(value, locale);

  return (
    <Card elevated>
      <div className={styles.body}>
        <p className={styles.title}>{title}</p>
        <div aria-live="polite" aria-busy={loading || undefined} className={styles.value}>
          {loading && value === undefined ? (
            <Skeleton className={styles.placeholder} />
          ) : (
            <span key={display} className={styles.figure}>
              {display}
            </span>
          )}
        </div>
        <p className={styles.help}>{help}</p>
      </div>
    </Card>
  );
}

export default KpiCard;

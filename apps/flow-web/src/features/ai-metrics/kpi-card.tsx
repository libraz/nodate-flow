/**
 * KpiCard — single headline metric tile for the AI metrics dashboard.
 *
 * Renders a small uppercase label and a large tabular-nums numeric
 * value inside the design-system {@link Card}. The value is formatted
 * with `Intl.NumberFormat` so locale-correct grouping separators are
 * applied (e.g. "1,234" / "1 234" / "1234").
 */

import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-metrics.module.css';

export interface KpiCardProps {
  label: string;
  value: number;
}

export default function KpiCard({ label, value }: KpiCardProps): ReactElement {
  const { i18n } = useTranslation('aiMetrics');
  const formatted = new Intl.NumberFormat(i18n.language).format(value);

  return (
    <Card className={styles.kpiCard}>
      <span className={styles.kpiLabel}>{label}</span>
      <span className={styles.kpiValue}>{formatted}</span>
    </Card>
  );
}

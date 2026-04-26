/**
 * AcceptanceCard — wider card showing the acceptance ratio as a
 * percentage plus a single horizontal bar for at-a-glance comparison.
 *
 * `acceptanceRate` from the API is in the [0, 1] range; the bar fill
 * width is clamped before being applied as `inline-size: <pct>%`.
 */

import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-metrics.module.css';

export interface AcceptanceCardProps {
  rate: number;
  applied: number;
  dismissed: number;
}

function clampRatio(raw: number): number {
  if (!Number.isFinite(raw)) return 0;
  if (raw < 0) return 0;
  if (raw > 1) return 1;
  return raw;
}

export default function AcceptanceCard({
  rate,
  applied,
  dismissed,
}: AcceptanceCardProps): ReactElement {
  const { t, i18n } = useTranslation('aiMetrics');
  const ratio = clampRatio(rate);
  const decided = applied + dismissed;

  const percentFmt = new Intl.NumberFormat(i18n.language, {
    style: 'percent',
    minimumFractionDigits: 0,
    maximumFractionDigits: 1,
  });
  const numberFmt = new Intl.NumberFormat(i18n.language);

  const percentLabel = percentFmt.format(ratio);
  const hint =
    decided > 0
      ? t('kpi.acceptanceHint', {
          applied: numberFmt.format(applied),
          decided: numberFmt.format(decided),
        })
      : t('kpi.noDecisions');

  return (
    <Card className={styles.acceptanceCard}>
      <div className={styles.acceptanceHeader}>
        <span className={styles.acceptanceLabel}>{t('kpi.acceptanceRate')}</span>
        <span className={styles.acceptanceValue}>{percentLabel}</span>
      </div>
      {/* biome-ignore lint/a11y/useFocusableInteractive: progressbar is a status role and does not require focus per WAI-ARIA */}
      <div
        className={styles.acceptanceTrack}
        role="progressbar"
        aria-label={t('kpi.acceptanceRate')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(ratio * 100)}
      >
        <div className={styles.acceptanceFill} style={{ inlineSize: `${ratio * 100}%` }} />
      </div>
      <span className={styles.acceptanceHint}>{hint}</span>
    </Card>
  );
}

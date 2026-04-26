/**
 * OutboundTable — per-provider egress rate-limit table for the AI
 * metrics dashboard.
 *
 * Renders one row per `OutboundLimitStat` returned by the metrics API
 * with `allowed` / `waited` / `denied` counters as right-aligned
 * tabular-nums numbers. When the input is empty (no providers tracked
 * yet within the window) the table is replaced by an empty-state
 * panel.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-metrics.module.css';
import type { OutboundLimitStat } from './api';

export interface OutboundTableProps {
  rows: readonly OutboundLimitStat[];
}

export default function OutboundTable({ rows }: OutboundTableProps): ReactElement {
  const { t, i18n } = useTranslation('aiMetrics');
  const numberFmt = new Intl.NumberFormat(i18n.language);

  return (
    <section className={styles.outboundSection} aria-labelledby="ai-metrics-outbound-title">
      <header className={styles.outboundHeader}>
        <h2 id="ai-metrics-outbound-title" className={styles.outboundTitle}>
          {t('outbound.title')}
        </h2>
        <p className={styles.outboundDescription}>{t('outbound.description')}</p>
      </header>
      {rows.length === 0 ? (
        <div className={styles.outboundEmpty}>{t('outbound.empty')}</div>
      ) : (
        <div className={styles.outboundTableWrap}>
          <table className={styles.outboundTable}>
            <thead>
              <tr>
                <th scope="col">{t('outbound.column.destination')}</th>
                <th scope="col" className={styles.numeric}>
                  {t('outbound.column.allowed')}
                </th>
                <th scope="col" className={styles.numeric}>
                  {t('outbound.column.waited')}
                </th>
                <th scope="col" className={styles.numeric}>
                  {t('outbound.column.denied')}
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.destination}>
                  <td>{row.destination}</td>
                  <td className={styles.numeric}>{numberFmt.format(row.allowed)}</td>
                  <td className={styles.numeric}>{numberFmt.format(row.waited)}</td>
                  <td className={styles.numeric}>{numberFmt.format(row.denied)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

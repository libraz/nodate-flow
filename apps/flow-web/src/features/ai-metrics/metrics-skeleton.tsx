/**
 * MetricsSkeleton — Suspense fallback for the AI metrics dashboard.
 *
 * Mirrors the final layout (3 KPI cards + acceptance card + outbound
 * table) so the surface stays stable when the suspense query resolves.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import styles from './ai-metrics.module.css';

export default function MetricsSkeleton(): ReactElement {
  return (
    <div
      aria-busy="true"
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}
    >
      <div className={styles.kpiGrid}>
        <Skeleton style={{ blockSize: '6.5rem', inlineSize: '100%' }} />
        <Skeleton style={{ blockSize: '6.5rem', inlineSize: '100%' }} />
        <Skeleton style={{ blockSize: '6.5rem', inlineSize: '100%' }} />
      </div>
      <Skeleton style={{ blockSize: '6.5rem', inlineSize: '100%' }} />
      <Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />
    </div>
  );
}

/**
 * WindowSelector — segmented control for the AI metrics rolling window.
 *
 * Wraps the design-system {@link SegmentedControl} primitive and emits
 * the next value as one of the supported {@link AiMetricsWindow}
 * presets (7 / 30 / 90). The label rendered before the segments comes
 * from the `aiMetrics` namespace so all visible text is translatable.
 */

import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-metrics.module.css';
import { type AiMetricsWindow, SUPPORTED_WINDOWS } from './api';

type SegmentValue = '7' | '30' | '90';

function toSegmentValue(window: AiMetricsWindow): SegmentValue {
  return String(window) as SegmentValue;
}

function fromSegmentValue(value: SegmentValue): AiMetricsWindow {
  return Number(value) as AiMetricsWindow;
}

export interface WindowSelectorProps {
  value: AiMetricsWindow;
  onChange: (next: AiMetricsWindow) => void;
}

export default function WindowSelector({ value, onChange }: WindowSelectorProps): ReactElement {
  const { t } = useTranslation('aiMetrics');

  const options: SegmentedControlOption<SegmentValue>[] = SUPPORTED_WINDOWS.map((days) => ({
    value: toSegmentValue(days),
    label: t(`window.${days}d`),
  }));

  return (
    <div className={styles.windowBar}>
      <span className={styles.windowLabel} id="ai-metrics-window-label">
        {t('window.label')}
      </span>
      <SegmentedControl<SegmentValue>
        ariaLabel={t('window.label')}
        options={options}
        value={toSegmentValue(value)}
        onChange={(next) => onChange(fromSegmentValue(next))}
        size="sm"
      />
    </div>
  );
}

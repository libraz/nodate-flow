/**
 * LabelLegend — one toggle chip per label present in the visible events,
 * plus a "No label" chip when unlabelled events sit alongside them. State
 * is the set of hidden keys, so a label that shows up later starts visible.
 * Events whose details are hidden from the viewer (`labelName` null) count
 * as unlabelled, which is what the viewer sees on them.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { ToggleChip } from '@nodate-flow/ui/primitives/toggle-chip';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './label-legend.module.css';

/** Hidden-set key for events without a visible label. */
export const NO_LABEL_KEY = '\u0000no-label';

/** The fields of a calendar event the legend reads. */
export interface LegendEvent {
  color: string;
  labelId?: string | null | undefined;
  labelName?: string | null | undefined;
}

export interface LegendEntry {
  key: string;
  name: string;
  color: string;
}

export interface LegendModel {
  labels: LegendEntry[];
  hasUnlabelled: boolean;
}

/** Key an event is filtered under: its label id, or {@link NO_LABEL_KEY}. */
export function eventLabelKey(ev: LegendEvent): string {
  return ev.labelId && ev.labelName ? ev.labelId : NO_LABEL_KEY;
}

/**
 * Legend contents for a set of events, or null when none carries a label
 * (a lone "No label" chip would filter nothing useful).
 */
export function deriveLegend(events: readonly LegendEvent[], locale: string): LegendModel | null {
  const byId = new Map<string, LegendEntry>();
  let hasUnlabelled = false;
  for (const ev of events) {
    const key = eventLabelKey(ev);
    if (key === NO_LABEL_KEY) {
      hasUnlabelled = true;
    } else if (!byId.has(key)) {
      byId.set(key, { key, name: ev.labelName ?? '', color: ev.color });
    }
  }
  if (byId.size === 0) return null;
  const labels = [...byId.values()].sort((a, b) => a.name.localeCompare(b.name, locale));
  return { labels, hasUnlabelled };
}

export interface LabelLegendProps {
  legend: LegendModel;
  hidden: ReadonlySet<string>;
  onHiddenChange: (next: Set<string>) => void;
}

/** Label chips that filter the calendar; see the file header. */
export default function LabelLegend({
  legend,
  hidden,
  onHiddenChange,
}: LabelLegendProps): ReactElement {
  const { t } = useTranslation('common');
  const captionId = useId();

  const handleToggle = (key: string, pressed: boolean): void => {
    const next = new Set(hidden);
    if (pressed) next.delete(key);
    else next.add(key);
    onHiddenChange(next);
  };

  return (
    <div className={styles.legend}>
      <span id={captionId} className={styles.caption}>
        {t('calendar.labelFilter.group')}
      </span>
      <div className={styles.chips}>
        {/* Own group element: ToggleChipGroup only takes aria-label, which would repeat the caption. */}
        <div role="group" aria-labelledby={captionId} className={styles.group}>
          {legend.labels.map((entry) => (
            <ToggleChip
              key={entry.key}
              pressed={!hidden.has(entry.key)}
              onPressedChange={(v) => handleToggle(entry.key, v)}
              color={entry.color}
              tone="tint"
            >
              {entry.name}
            </ToggleChip>
          ))}
          {legend.hasUnlabelled ? (
            <ToggleChip
              pressed={!hidden.has(NO_LABEL_KEY)}
              onPressedChange={(v) => handleToggle(NO_LABEL_KEY, v)}
              color="var(--nf-color-fg-muted)"
              tone="tint"
            >
              {t('calendar.labelFilter.none')}
            </ToggleChip>
          ) : null}
        </div>
        {hidden.size > 0 ? (
          <Button type="button" variant="ghost" size="sm" onClick={() => onHiddenChange(new Set())}>
            {t('calendar.labelFilter.showAll')}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

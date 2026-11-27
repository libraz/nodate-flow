import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import type { Dispatch, ReactElement, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './calendar-page.module.css';
import { eventKindSampleClassName } from './event-pill';

export interface LayerFlags {
  tasksDue: boolean;
  events: boolean;
  blocks: boolean;
  free: boolean;
  milestone: boolean;
  holidays: boolean;
}

interface CalendarLayerChipsProps {
  layers: LayerFlags;
  onLayersChange: Dispatch<SetStateAction<LayerFlags>>;
  /** Whether the holidays chip is offered (the viewer has a country set). */
  showHolidays: boolean;
}

/** The toggle chips that switch each calendar layer on or off. */
function CalendarLayerChips({
  layers,
  onLayersChange: setLayers,
  showHolidays,
}: CalendarLayerChipsProps): ReactElement {
  const { t } = useTranslation('common');
  // Quiet tinted chips: the swatch carries each layer's colour, so the
  // toggles do not outshout the grid they filter.
  return (
    <ToggleChipGroup label={t('calendar.layers')}>
      <ToggleChip
        pressed={layers.tasksDue}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, tasksDue: v }))}
        color="var(--nf-cal-task-color)"
      >
        {t('calendar.layer.tasks_due')}
      </ToggleChip>
      <ToggleChip
        pressed={layers.events}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, events: v }))}
        label={t('calendar.layer.events')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="event" />
          {t('calendar.layer.events')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.blocks}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, blocks: v }))}
        label={t('calendar.layer.blocks')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="block" />
          {t('calendar.layer.blocks')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.free}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, free: v }))}
        label={t('calendar.layer.free')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="free" />
          {t('calendar.layer.free')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.milestone}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, milestone: v }))}
        label={t('calendar.layer.milestone')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="milestone" />
          {t('calendar.layer.milestone')}
        </span>
      </ToggleChip>
      {showHolidays ? (
        <ToggleChip
          pressed={layers.holidays}
          tone="tint"
          onPressedChange={(v) => setLayers((s) => ({ ...s, holidays: v }))}
          color="var(--nf-color-danger)"
        >
          {t('calendar.layer.holidays')}
        </ToggleChip>
      ) : null}
    </ToggleChipGroup>
  );
}

export default CalendarLayerChips;

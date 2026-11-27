import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import type { BlockPreset } from './event-dialog';
import styles from './event-dialog.module.css';

const BLOCK_PRESET_KEYS = {
  working: 'blockLabel.preset.working',
  focus: 'blockLabel.preset.focus',
  oof: 'blockLabel.preset.oof',
  custom: 'blockLabel.preset.custom',
} as const satisfies Record<BlockPreset, string>;

interface EventDialogBlockFieldsProps {
  blockPreset: BlockPreset;
  onBlockPresetChange: (value: BlockPreset) => void;
  blockCustomLabel: string;
  onBlockCustomLabelChange: (value: string) => void;
}

/** Block-kind fields: preset chips and the custom label input. */
export default function EventDialogBlockFields({
  blockPreset,
  onBlockPresetChange,
  blockCustomLabel,
  onBlockCustomLabelChange,
}: EventDialogBlockFieldsProps): ReactElement {
  const { t } = useTranslation('calendar-events');

  return (
    <div className={styles.kindBlock}>
      <FormField label={t('field.blockLabel')} description={t('field.blockLabelHint')}>
        {() => (
          <>
            <ToggleChipGroup label={t('field.blockLabel')}>
              {(['working', 'focus', 'oof', 'custom'] as const).map((preset) => (
                <ToggleChip
                  key={preset}
                  pressed={blockPreset === preset}
                  onPressedChange={(v) => {
                    if (!v) return;
                    onBlockPresetChange(preset);
                  }}
                >
                  {t(BLOCK_PRESET_KEYS[preset])}
                </ToggleChip>
              ))}
            </ToggleChipGroup>
            {blockPreset === 'custom' ? (
              <Input
                value={blockCustomLabel}
                onChange={(e) => {
                  onBlockCustomLabelChange(e.currentTarget.value);
                }}
                className={styles.blockCustomInput}
              />
            ) : null}
          </>
        )}
      </FormField>
    </div>
  );
}

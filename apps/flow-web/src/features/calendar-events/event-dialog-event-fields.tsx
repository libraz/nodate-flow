import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import type { Flexibility, ShowAs } from './event-dialog';
import styles from './event-dialog.module.css';

const SHOW_AS_KEYS = {
  busy: 'showAs.busy',
  free: 'showAs.free',
  tentative: 'showAs.tentative',
  oof: 'showAs.oof',
} as const satisfies Record<ShowAs, string>;

const FLEXIBILITY_KEYS = {
  fixed: 'flexibility.fixed',
  negotiable: 'flexibility.negotiable',
  conditional: 'flexibility.conditional',
} as const satisfies Record<Flexibility, string>;

interface EventDialogEventFieldsProps {
  showAs: ShowAs;
  onShowAsChange: (value: ShowAs) => void;
  flexibility: Flexibility;
  onFlexibilityChange: (value: Flexibility) => void;
  location: string;
  onLocationChange: (value: string) => void;
  detailsHidden: boolean;
}

/** Event-kind fields: free/busy, flexibility and location. */
export default function EventDialogEventFields({
  showAs,
  onShowAsChange,
  flexibility,
  onFlexibilityChange,
  location,
  onLocationChange,
  detailsHidden,
}: EventDialogEventFieldsProps): ReactElement {
  const { t } = useTranslation('calendar-events');

  return (
    <div className={styles.kindBlock}>
      <FormField label={t('field.showAs')} description={t('field.showAsHint')}>
        {() => (
          <SegmentedControl
            ariaLabel={t('field.showAs')}
            fullWidth
            options={(['busy', 'free', 'tentative', 'oof'] as const).map((v) => ({
              value: v,
              label: t(SHOW_AS_KEYS[v]),
            }))}
            value={showAs}
            onChange={onShowAsChange}
          />
        )}
      </FormField>
      <FormField label={t('field.flexibility')} description={t('field.flexibilityHint')}>
        {() => (
          <SegmentedControl
            ariaLabel={t('field.flexibility')}
            fullWidth
            options={(['fixed', 'negotiable', 'conditional'] as const).map((v) => ({
              value: v,
              label: t(FLEXIBILITY_KEYS[v]),
            }))}
            value={flexibility}
            onChange={onFlexibilityChange}
          />
        )}
      </FormField>
      <FormField label={t('field.location')}>
        {(control) => (
          <Input
            {...control}
            value={location}
            disabled={detailsHidden}
            onChange={(e) => {
              onLocationChange(e.currentTarget.value);
            }}
          />
        )}
      </FormField>
    </div>
  );
}

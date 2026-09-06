/**
 * RecurringScopeDialog — the "this event / this and following / all
 * events" choice a repeating calendar event has to answer before a save
 * or a delete is committed.
 *
 * Asked at the moment of consequence rather than as a control inside the
 * edit form. A selector in the form carries a default nobody looked at,
 * and on a form the user only meant to fix a title on, the pre-selected
 * value would be the one that rewrites — or removes — every occurrence
 * in the series. Raising the question on commit makes the decision
 * unavoidable and puts it next to the action it governs.
 *
 * For delete this step *replaces* the ordinary confirm: the choice is
 * already a confirmation, and stacking two of them trains people to
 * dismiss both.
 *
 * Each option states what it does to the series rather than only naming
 * itself, because "All events" alone does not say that occurrences
 * already in the past go with it.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Radio from '@nodate-flow/ui/primitives/radio';
import { type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { RecurrenceScope } from './api';
import styles from './recurring-scope-dialog.module.css';

/** Which commit the choice is being asked for. */
export type RecurringScopeAction = 'save' | 'delete';

/**
 * Least destructive first. The order is also the reading order of the
 * blast radius — one occurrence, then the tail, then the whole series.
 */
const SCOPE_ORDER: readonly RecurrenceScope[] = ['occurrence', 'thisAndFollowing', 'series'];

/**
 * Static key tables. Every key stays a literal so `i18next-parser` can
 * extract it and the locale-sync check can hold en / ja / zh together.
 */
const OPTION_KEYS = {
  occurrence: {
    label: 'recurrence.scope.option.occurrence.label',
    save: 'recurrence.scope.option.occurrence.save',
    delete: 'recurrence.scope.option.occurrence.delete',
  },
  thisAndFollowing: {
    label: 'recurrence.scope.option.thisAndFollowing.label',
    save: 'recurrence.scope.option.thisAndFollowing.save',
    delete: 'recurrence.scope.option.thisAndFollowing.delete',
  },
  series: {
    label: 'recurrence.scope.option.series.label',
    save: 'recurrence.scope.option.series.save',
    delete: 'recurrence.scope.option.series.delete',
  },
} as const satisfies Record<RecurrenceScope, Record<'label' | RecurringScopeAction, string>>;

const ACTION_KEYS = {
  save: {
    title: 'recurrence.scope.save.title',
    legend: 'recurrence.scope.save.legend',
    confirm: 'recurrence.scope.save.confirm',
  },
  delete: {
    title: 'recurrence.scope.delete.title',
    legend: 'recurrence.scope.delete.legend',
    confirm: 'recurrence.scope.delete.confirm',
  },
} as const satisfies Record<RecurringScopeAction, Record<'title' | 'legend' | 'confirm', string>>;

export interface RecurringScopeDialogProps {
  /** Whether the choice is being asked. */
  open: boolean;
  /** Which commit is waiting on the answer. */
  action: RecurringScopeAction;
  /** True while the write this choice releases is in flight. */
  pending: boolean;
  /** Dismiss without acting — Escape, the overlay, or the cancel button. */
  onCancel: () => void;
  /** Commit with the chosen scope. */
  onConfirm: (scope: RecurrenceScope) => void;
}

export default function RecurringScopeDialog({
  open,
  action,
  pending,
  onCancel,
  onConfirm,
}: RecurringScopeDialogProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const groupName = useId();
  // Defaults to the single occurrence: the option that changes the least
  // is the one a mis-click can afford.
  const [scope, setScope] = useState<RecurrenceScope>('occurrence');

  return (
    <Dialog open={open} onClose={onCancel} title={t(ACTION_KEYS[action].title)} size="sm">
      <div className={styles.body}>
        <fieldset className={styles.group}>
          <legend className={styles.legend}>{t(ACTION_KEYS[action].legend)}</legend>
          {SCOPE_ORDER.map((value) => {
            const inputId = `${groupName}-${value}`;
            const labelId = `${inputId}-label`;
            const hintId = `${inputId}-hint`;
            return (
              <label key={value} htmlFor={inputId} className={styles.option}>
                {/*
                 * Named by the option line and described by the sentence
                 * under it. Left to the wrapping label, the accessible
                 * name would be both of them run together, and the
                 * consequence would then also be read a second time as
                 * the description.
                 */}
                <Radio
                  id={inputId}
                  name={groupName}
                  value={value}
                  checked={scope === value}
                  onChange={() => {
                    setScope(value);
                  }}
                  aria-labelledby={labelId}
                  aria-describedby={hintId}
                  disabled={pending}
                />
                <span className={styles.optionBody}>
                  <span id={labelId} className={styles.optionLabel}>
                    {t(OPTION_KEYS[value].label)}
                  </span>
                  <span id={hintId} className={styles.optionHint}>
                    {t(OPTION_KEYS[value][action])}
                  </span>
                </span>
              </label>
            );
          })}
        </fieldset>

        <div className={styles.actions}>
          <Button type="button" variant="ghost" onClick={onCancel} disabled={pending}>
            {t('recurrence.scope.cancel')}
          </Button>
          <Button
            type="button"
            variant={action === 'delete' ? 'danger' : 'primary'}
            disabled={pending}
            onClick={() => {
              onConfirm(scope);
            }}
          >
            {t(ACTION_KEYS[action].confirm)}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

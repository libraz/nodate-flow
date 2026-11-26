/**
 * LabelPicker — the event dialog's "Label" field: a radiogroup of chips,
 * "None" first, then one per label on the calendar. Renders nothing when
 * the calendar has no labels or the viewer cannot see the event's details.
 * An empty `value` means "no label".
 */

import { type KeyboardEvent, type ReactElement, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { FALLBACK_EVENT_COLOR, isHexColor } from '../../lib/source-colors';
import { useCalendarLabelsQuery } from '../calendars/labels-api';
import { eventColorStyle } from './event-pill';
import styles from './label-picker.module.css';

export interface LabelPickerProps {
  workspaceId: string;
  calendarId: string;
  /** Selected label id, or '' for none. */
  value: string;
  onChange: (labelId: string) => void;
  /** Colour the event shows without a label; falls back when not a hex colour. */
  noneColor: string | undefined;
  detailsHidden?: boolean;
  /** `false` disables the control and says why. */
  canEdit?: boolean;
  /** Already-translated error shown under the field. */
  error?: string | null;
}

interface Choice {
  id: string;
  name: string;
  color: string;
}

export default function LabelPicker({
  workspaceId,
  calendarId,
  value,
  onChange,
  noneColor,
  detailsHidden = false,
  canEdit,
  error,
}: LabelPickerProps): ReactElement | null {
  const { t } = useTranslation('calendar-events');
  const labelsQuery = useCalendarLabelsQuery(workspaceId, calendarId);
  const nameId = useId();
  const hintId = useId();
  const chipRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const labels = labelsQuery.data ?? [];
  if (detailsHidden || labels.length === 0) return null;

  const disabled = canEdit === false;
  const choices: Choice[] = [
    {
      id: '',
      name: t('field.label_none'),
      color: noneColor !== undefined && isHexColor(noneColor) ? noneColor : FALLBACK_EVENT_COLOR,
    },
    ...labels.map((l) => ({ id: l.id, name: l.name, color: l.color })),
  ];
  const found = choices.findIndex((c) => c.id === value);
  const selectedIndex = Math.max(0, found);

  function handleKeyDown(e: KeyboardEvent<HTMLButtonElement>, index: number): void {
    let next: number;
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        next = (index + 1) % choices.length;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        next = (index - 1 + choices.length) % choices.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = choices.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    const target = choices[next];
    if (target === undefined) return;
    onChange(target.id);
    chipRefs.current[next]?.focus();
  }

  return (
    <div className={styles.root}>
      <span id={nameId} className={styles.label}>
        {t('field.label')}
      </span>
      <div
        role="radiogroup"
        aria-labelledby={nameId}
        aria-describedby={disabled ? hintId : undefined}
        aria-disabled={disabled || undefined}
        className={styles.group}
      >
        {choices.map((c, i) => (
          <button
            key={c.id === '' ? 'none' : c.id}
            ref={(el) => {
              chipRefs.current[i] = el;
            }}
            type="button"
            role="radio"
            aria-checked={i === selectedIndex}
            tabIndex={i === selectedIndex ? 0 : -1}
            disabled={disabled}
            className={styles.chip}
            style={eventColorStyle(c.color)}
            onClick={() => onChange(c.id)}
            onKeyDown={(e) => handleKeyDown(e, i)}
          >
            <span aria-hidden="true" className={styles.dot} />
            <span className={styles.name}>{c.name}</span>
          </button>
        ))}
      </div>
      {disabled ? (
        <p id={hintId} className={styles.hint}>
          {t('field.label_disabled')}
        </p>
      ) : null}
      <div aria-live="polite">{error ? <p className={styles.error}>{error}</p> : null}</div>
    </div>
  );
}

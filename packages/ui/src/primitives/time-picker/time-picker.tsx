/**
 * TimePicker — dropdown for selecting a time of day.
 *
 * Renders a trigger button that opens a scrollable list of time slots.
 * Fully controlled via `value` / `onChange` with string format "HH:MM".
 */

import { type ReactElement, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Popover from '../popover/popover';
import styles from './time-picker.module.css';

export interface TimePickerProps {
  /** Current value in "HH:MM" format. */
  value: string;
  /** Called when the user selects a time slot. */
  onChange: (time: string) => void;
  /** Interval between slots in minutes. Defaults to 15. */
  step?: number;
  /** Custom trigger label. Defaults to the value. */
  triggerLabel?: string;
  /** Additional class on the trigger button. */
  className?: string;
}

function generateSlots(step: number): string[] {
  const slots: string[] = [];
  for (let h = 0; h < 24; h++) {
    for (let m = 0; m < 60; m += step) {
      slots.push(`${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`);
    }
  }
  return slots;
}

function nearestSlot(value: string, step: number): string {
  const [h, m] = value.split(':').map(Number);
  const totalMin = (h ?? 0) * 60 + (m ?? 0);
  const maxMin = 23 * 60 + 60 - step;
  const rounded = Math.min(Math.round(totalMin / step) * step, maxMin);
  const rh = Math.floor(rounded / 60);
  const rm = rounded % 60;
  return `${String(rh).padStart(2, '0')}:${String(rm).padStart(2, '0')}`;
}

/** TimePicker renders a popover with scrollable time slots. */
export default function TimePicker({
  value,
  onChange,
  step = 15,
  triggerLabel,
  className,
}: TimePickerProps): ReactElement {
  const [open, setOpen] = useState(false);
  const selectedRef = useRef<HTMLButtonElement>(null);
  const slots = useMemo(() => generateSlots(step), [step]);
  const nearest = useMemo(() => nearestSlot(value, step), [value, step]);

  const handleSelect = useCallback(
    (slot: string) => {
      onChange(slot);
      setOpen(false);
    },
    [onChange],
  );

  // Scroll selected slot into view when opening
  useEffect(() => {
    if (open) {
      requestAnimationFrame(() => {
        selectedRef.current?.scrollIntoView({ block: 'center' });
      });
    }
  }, [open]);

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-start"
      content={
        <div className={styles.list} role="listbox" aria-label="Time">
          {slots.map((slot) => {
            const isSelected = slot === nearest;
            return (
              <button
                key={slot}
                ref={isSelected ? selectedRef : undefined}
                type="button"
                role="option"
                aria-selected={isSelected}
                data-selected={isSelected || undefined}
                className={styles.slot}
                onClick={() => handleSelect(slot)}
              >
                {slot}
              </button>
            );
          })}
        </div>
      }
    >
      <button
        type="button"
        className={className ? `${styles.trigger} ${className}` : styles.trigger}
      >
        {triggerLabel ?? (value || '00:00')}
      </button>
    </Popover>
  );
}

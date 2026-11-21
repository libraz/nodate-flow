/**
 * SettingRow: the one row shape for a single setting. The label and its
 * description sit on the inline-start side, the control on the inline-end
 * side; the render prop receives the id and description wiring for it.
 * The `list` variant is for rows stacked inside a bordered list: padded,
 * with a hover tint, the divider left to the list.
 */

import { type ReactElement, type ReactNode, useId } from 'react';

import ui from './settings-ui.module.css';

export interface SettingRowControlProps {
  id: string;
  'aria-describedby'?: string;
}

export interface SettingRowProps {
  label: ReactNode;
  description?: ReactNode;
  variant?: 'plain' | 'list';
  children: (control: SettingRowControlProps) => ReactNode;
}

export default function SettingRow({
  label,
  description,
  variant = 'plain',
  children,
}: SettingRowProps): ReactElement {
  const id = useId();
  const descId = `${id}-desc`;
  const control: SettingRowControlProps = description ? { id, 'aria-describedby': descId } : { id };
  return (
    <div className={variant === 'list' ? ui.settingRowList : ui.settingRow}>
      <div className={ui.settingText}>
        <label htmlFor={id} className={ui.settingLabel}>
          {label}
        </label>
        {description ? (
          <p id={descId} className={ui.settingDescription}>
            {description}
          </p>
        ) : null}
      </div>
      <div className={ui.settingControl}>{children(control)}</div>
    </div>
  );
}

/**
 * PriorityPicker — the priority of a task as an editable value: colour mark,
 * label and a chevron; opens the shared PriorityControl in a popover and
 * closes once a rank is chosen.
 */

import Popover from '@nodate-flow/ui/primitives/popover';
import { ChevronDown } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TaskPriority } from './api';
import { PRIORITY_KEY } from './constants';
import PriorityControl from './priority-control';
import PriorityMark from './priority-mark';
import field from './property-field.module.css';

export interface PriorityPickerProps {
  value: TaskPriority;
  onChange: (next: TaskPriority) => void;
}

export default function PriorityPicker({ value, onChange }: PriorityPickerProps): ReactElement {
  const { t } = useTranslation('common');
  const [open, setOpen] = useState(false);
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      placement="bottom-start"
      minInlineSize="20rem"
      ariaLabel={t('tasks.priority.aria_label')}
      content={
        <PriorityControl
          value={value}
          onChange={(next) => {
            onChange(next);
            setOpen(false);
          }}
        />
      }
    >
      <button type="button" className={field.field} aria-haspopup="dialog" aria-expanded={open}>
        <PriorityMark priority={value} />
        <span>{t(PRIORITY_KEY[value])}</span>
        <ChevronDown size={14} aria-hidden className={field.icon} />
      </button>
    </Popover>
  );
}

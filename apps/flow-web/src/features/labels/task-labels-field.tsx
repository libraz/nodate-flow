/**
 * TaskLabelsField — label chips plus the picker for one task.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { type ReactElement, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import {
  useAddTaskLabel,
  useCreateLabel,
  useLabelsQuery,
  useRemoveTaskLabel,
  useTaskLabelsQuery,
} from './api';
import LabelBadge from './label-badge';
import { pickLabelColor } from './label-color';
import LabelPicker from './label-picker';
import styles from './task-labels-field.module.css';

export interface TaskLabelsFieldProps {
  taskId: string;
  workspaceId: string;
  /** Id of the element that names this field; set on the chip list. */
  labelledBy?: string;
}

/** Suspense fallback: one pill-height row. */
export function TaskLabelsFieldSkeleton(): ReactElement {
  return <Skeleton className={styles.skeleton} />;
}

export default function TaskLabelsField({
  taskId,
  workspaceId,
  labelledBy,
}: TaskLabelsFieldProps): ReactElement {
  const { t } = useTranslation('labels');
  const { data: all } = useLabelsQuery(workspaceId);
  const { data: applied } = useTaskLabelsQuery(taskId);
  const { entries, onExited } = usePresenceList(applied, (l) => l.id);
  const create = useCreateLabel();
  const add = useAddTaskLabel();
  const remove = useRemoveTaskLabel();

  const fail = (err: unknown, key: string): void => {
    toaster.show({ tone: 'danger', message: formatApiError(err, t, key) });
  };

  const chips = useRef(new Map<string, HTMLSpanElement>());
  const pickerItem = useRef<HTMLLIElement>(null);

  /** After a keyboard removal, focus the next chip's remove button, else the previous, else the add trigger. */
  const moveFocusFrom = (labelId: string): void => {
    const removeButton = (id: string): HTMLButtonElement | null | undefined =>
      chips.current.get(id)?.querySelector('button');
    if (!removeButton(labelId)?.matches(':focus-visible')) return;
    const index = applied.findIndex((l) => l.id === labelId);
    const neighbour = applied[index + 1] ?? applied[index - 1];
    const target = neighbour
      ? removeButton(neighbour.id)
      : pickerItem.current?.querySelector('button');
    target?.focus();
  };

  const handleRemove = (labelId: string): void => {
    moveFocusFrom(labelId);
    handleToggle(labelId, false);
  };

  const handleToggle = (labelId: string, active: boolean): void => {
    const mutation = active ? add : remove;
    mutation.mutate(
      { taskId, labelId },
      { onError: (err) => fail(err, active ? 'errors.add_failed' : 'errors.remove_failed') },
    );
  };

  const handleCreate = async (name: string): Promise<void> => {
    let created: { id: string };
    try {
      created = await create.mutateAsync({
        workspaceId,
        input: { name, color: pickLabelColor(name) },
      });
    } catch (err) {
      fail(err, 'errors.create_failed');
      return;
    }
    try {
      await add.mutateAsync({ taskId, labelId: created.id });
    } catch (err) {
      fail(err, 'errors.add_failed');
    }
  };

  return (
    <ul className={styles.chips} aria-labelledby={labelledBy}>
      {entries.map(({ key, item: label, present }) => (
        <PresenceItem
          key={key}
          as="li"
          present={present}
          onExited={() => onExited(key)}
          className={motion.enter}
        >
          <LabelBadge
            name={label.name}
            color={label.color}
            onRemove={() => handleRemove(label.id)}
            ref={(el) => {
              if (el) chips.current.set(label.id, el);
              else chips.current.delete(label.id);
            }}
          />
        </PresenceItem>
      ))}
      <li ref={pickerItem}>
        <LabelPicker
          labels={all}
          activeIds={applied.map((l) => l.id)}
          onToggle={handleToggle}
          onCreate={(name) => {
            void handleCreate(name);
          }}
          disabled={create.isPending}
        />
      </li>
    </ul>
  );
}

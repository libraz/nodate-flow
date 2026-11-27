/**
 * TaskLabelsField — label chips plus the picker for one task.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import type { ReactElement } from 'react';
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
}

export default function TaskLabelsField({
  taskId,
  workspaceId,
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
    <ul className={styles.chips}>
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
            onRemove={() => handleToggle(label.id, false)}
          />
        </PresenceItem>
      ))}
      <li>
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

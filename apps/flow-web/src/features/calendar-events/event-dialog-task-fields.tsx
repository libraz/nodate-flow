import type { components } from '@nodate-flow/sdk';
import FormField from '@nodate-flow/ui/primitives/form-field';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import Select from '@nodate-flow/ui/primitives/select';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { TASK_PRIORITIES, type TaskPriority } from '../tasks/api';
import { PRIORITY_KEY } from '../tasks/constants';
import styles from './event-dialog.module.css';

type FlowProject = components['schemas']['Project'];

interface EventDialogTaskFieldsProps {
  projects: FlowProject[];
  projectId: string;
  onProjectChange: (value: string) => void;
  projectError: string | null;
  priority: TaskPriority;
  onPriorityChange: (value: TaskPriority) => void;
}

/** Task-kind fields: project select and priority. */
export default function EventDialogTaskFields({
  projects,
  projectId,
  onProjectChange,
  projectError,
  priority,
  onPriorityChange,
}: EventDialogTaskFieldsProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const { t: tCommon } = useTranslation('common');

  return (
    <div className={styles.kindBlock}>
      <div className={styles.inlineRow}>
        {projects.length > 0 ? (
          <FormField
            label={t('field.project')}
            description={t('field.projectHint')}
            error={projectError ?? undefined}
            className={styles.inlineGrow}
          >
            {(control) => (
              <Select
                {...control}
                value={projectId}
                onChange={(e) => {
                  onProjectChange(e.currentTarget.value);
                }}
              >
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
        ) : null}
        <FormField label={t('field.priority')} className={styles.inlineGrow}>
          {() => (
            <SegmentedControl
              ariaLabel={t('field.priority')}
              options={TASK_PRIORITIES.map((p) => ({
                value: String(p) as `${TaskPriority}`,
                label: tCommon(PRIORITY_KEY[p]),
              }))}
              value={String(priority) as `${TaskPriority}`}
              onChange={(v) => {
                onPriorityChange(Number(v) as TaskPriority);
              }}
            />
          )}
        </FormField>
      </div>
    </div>
  );
}
